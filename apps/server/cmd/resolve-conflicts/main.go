// cmd/resolve-conflicts resolves a reviewed list of open conflicts with the
// "keep current" strategy: the live file stays, the loser copy is deleted and
// its bytes are released from the owner's quota.
//
// It runs the same coordinator code as the web and desktop clients
// (sync.Impl.ResolveConflict), so quota accounting and storage cleanup are
// identical to resolving by hand. It starts no outbox worker: rows it enqueues
// are delivered by the running server.
//
// Dry run is the default and changes nothing. Pass -apply to resolve.
//
// Usage:
//
//	resolve-conflicts -db postgres://… -data /data -user <owner-uuid> -ids ids.txt [-apply]
//
// ids.txt holds one conflict ID per line; blank lines and #-comments are
// ignored. A conflict that is already resolved, unknown or owned by another
// user is reported and skipped.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/bus"
	"privasync/server/internal/config"
	"privasync/server/internal/db"
	"privasync/server/internal/mail"
	"privasync/server/internal/outbox"
	"privasync/server/internal/quota"
	"privasync/server/internal/repository"
	"privasync/server/internal/storage"
	syncpkg "privasync/server/internal/sync"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "resolve-conflicts failed:", err)
		os.Exit(1)
	}
}

func run() error {
	dbURL := flag.String("db", os.Getenv("DATABASE_URL"), "PostgreSQL connection URL (default: DATABASE_URL)")
	dataPath := flag.String("data", "/data", "storage data path")
	userFlag := flag.String("user", "", "owner user ID (required)")
	idsFile := flag.String("ids", "", "file with one conflict ID per line (required)")
	apply := flag.Bool("apply", false, "resolve for real; without it nothing is changed")
	flag.Parse()

	if *dbURL == "" || *userFlag == "" || *idsFile == "" {
		return errors.New("-db (or DATABASE_URL), -user and -ids are required")
	}
	userID, err := uuid.Parse(*userFlag)
	if err != nil {
		return fmt.Errorf("invalid -user: %w", err)
	}
	ids, err := readIDs(*idsFile)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool, err := db.NewPool(ctx, config.DatabaseConfig{URL: *dbURL, MaxConns: 4, MinConns: 1})
	if err != nil {
		return fmt.Errorf("connecting to database: %w", err)
	}
	defer pool.Close()

	stor := storage.NewService(*dataPath)
	users := repository.NewUserRepository(pool, stor)
	conflicts := repository.NewConflictRepository(pool)

	mailer, err := mail.New(config.SMTPConfig{}) // disabled: sends nothing
	if err != nil {
		return err
	}
	outboxRepo := outbox.NewRepository(pool)
	domainBus := bus.New(bus.Config{})
	defer domainBus.Close()
	quotaSvc := quota.New(users, pool, mailer, "")
	bus.WireSubscribers(domainBus, quotaSvc, mail.NewSubscriber(outboxRepo, users), syncpkg.NewQuotaSSESubscriber(outboxRepo))

	// The worker is only a wake-up target for the coordinator; it never runs
	// here, so no outbox row is processed by this process.
	worker := outbox.NewWorker(outboxRepo, map[outbox.Kind]outbox.Handler{})
	coord := syncpkg.NewCoordinator(pool,
		repository.NewFileRepository(pool),
		repository.NewSyncRootRepository(pool, stor),
		conflicts,
		repository.NewTrashRepository(pool),
		outboxRepo, worker, quotaSvc, domainBus, stor)

	var resolved, skipped, failed int
	var freed int64
	for _, id := range ids {
		d, err := conflicts.FindOpenConflictDetail(ctx, id)
		if errors.Is(err, repository.ErrNotFound) {
			fmt.Printf("SKIP    %s  not found or already resolved\n", id)
			skipped++
			continue
		}
		if err != nil {
			fmt.Printf("FAIL    %s  %v\n", id, err)
			failed++
			continue
		}
		if d.OwnerID != userID {
			fmt.Printf("SKIP    %s  owned by another user\n", id)
			skipped++
			continue
		}
		size, _ := stor.FileSize(d.OwnerID, d.SyncRootID.String(), d.ConflictPath)

		if !*apply {
			fmt.Printf("WOULD   %s  %d bytes  %s\n", id, size, d.ConflictPath)
			resolved++
			freed += size
			continue
		}
		tctx, tcancel := context.WithTimeout(ctx, 2*time.Minute)
		res, err := coord.ResolveConflict(tctx, syncpkg.ResolveConflictCmd{
			UserID: userID, ConflictID: id, Strategy: syncpkg.StrategyKeepCurrent,
		})
		tcancel()
		if err != nil {
			fmt.Printf("FAIL    %s  %v\n", id, err)
			failed++
			continue
		}
		fmt.Printf("OK      %s  freed %d bytes  %s\n", id, res.FreedBytes, d.ConflictPath)
		resolved++
		freed += res.FreedBytes
	}

	verb := "resolved"
	if !*apply {
		verb = "would resolve (dry run)"
	}
	fmt.Printf("\n%s: %d, skipped: %d, failed: %d, bytes freed: %d (%.2f GB)\n",
		verb, resolved, skipped, failed, freed, float64(freed)/(1<<30))
	if failed > 0 {
		return fmt.Errorf("%d conflicts failed", failed)
	}
	return nil
}

func readIDs(path string) ([]uuid.UUID, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var ids []uuid.UUID
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		id, err := uuid.Parse(line)
		if err != nil {
			return nil, fmt.Errorf("invalid conflict ID %q: %w", line, err)
		}
		ids = append(ids, id)
	}
	return ids, sc.Err()
}
