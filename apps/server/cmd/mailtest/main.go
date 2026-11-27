// Ops-Tool: liest die Server-Config, verifiziert die SMTP-Verbindung und
// verschickt optional eine Test-Mail. Aufruf:
//
//	go run ./cmd/mailtest -config /config/config.yaml [-to name@host]
//
// Ohne -to wird nur DialWithContext geprobt (Connection-Check).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"privasync/server/internal/config"
	"privasync/server/internal/mail"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	configPath := flag.String("config", "", "Path to config file (optional)")
	to := flag.String("to", "", "Recipient address (leave empty for connection-check only)")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fatal("loading config: %v", err)
	}
	if !cfg.SMTP.Enabled {
		fatal("smtp.enabled=false — aktiviere SMTP in der Config und versuche es erneut")
	}

	mailer, err := mail.New(cfg.SMTP)
	if err != nil {
		fatal("mailer init: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	fmt.Printf("→ Dial %s:%d (tls_mode=%s) …\n", cfg.SMTP.Host, cfg.SMTP.Port, cfg.SMTP.TLSMode)
	if err := mailer.VerifyConnection(ctx); err != nil {
		fatal("connection failed: %v", err)
	}
	fmt.Println("✓ SMTP-Verbindung ok")

	if *to == "" {
		fmt.Println("(kein -to angegeben — Connection-Check erfolgreich, keine Test-Mail gesendet)")
		return
	}

	mailer.Start(ctx)
	defer mailer.Stop()

	if err := mailer.Send(&mail.Message{
		To:       *to,
		Subject:  "PrivaSync — SMTP-Testmail",
		BodyText: "Wenn du diese Mail liest, ist dein SMTP-Setup korrekt konfiguriert.\n\n— PrivaSync",
		BodyHTML: "<p>Wenn du diese Mail liest, ist dein SMTP-Setup korrekt konfiguriert.</p><p>— PrivaSync</p>",
	}); err != nil {
		fatal("enqueue failed: %v", err)
	}
	fmt.Printf("→ Test-Mail an %s in die Queue gelegt — warte auf Versand …\n", *to)
	// Let the worker drain before Stop() runs via defer.
	time.Sleep(3 * time.Second)
	fmt.Println("✓ Fertig")
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "mailtest: "+format+"\n", args...)
	os.Exit(1)
}
