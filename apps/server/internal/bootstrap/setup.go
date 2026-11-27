// Package bootstrap handles one-shot admin setup on first container start.
//
// When no admin exists, a cryptographically random setup token is generated,
// printed to stdout (visible via `docker logs`), and kept in-memory. The
// operator visits /setup?token=<token> and creates the admin account. After
// success the token is zeroed and subsequent /api/v1/setup requests return 404.
//
// Rationale (vs. env-var bootstrap): admin password never lives in the
// container env, docker inspect output, or IaC state. Race-safe because no
// unauthenticated attacker without stdout access can complete setup.
package bootstrap

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	"privasync/server/internal/repository"
)

// State is the in-memory setup state. Safe for concurrent use.
type State struct {
	mu    sync.Mutex
	token string // empty once consumed or when no setup is needed
}

// Init inspects the users table and returns a State. When no admin exists,
// it generates a setup token and logs it. Token is zeroed on success via
// Complete.
func Init(ctx context.Context, users *repository.UserRepository) (*State, error) {
	hasAdmin, err := users.HasAdmin(ctx)
	if err != nil {
		return nil, fmt.Errorf("checking for admin: %w", err)
	}
	s := &State{}
	if hasAdmin {
		return s, nil
	}

	token, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("generating setup token: %w", err)
	}
	s.token = token
	logBanner(token)
	return s, nil
}

// NeedsSetup returns true when the server is waiting for admin creation.
func (s *State) NeedsSetup() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.token != ""
}

// Verify constant-time compares the supplied token against the active one.
// Returns false when setup is already complete or the token mismatches.
func (s *State) Verify(supplied string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(s.token), []byte(supplied)) == 1
}

// Complete zeros the token. Callers must have verified the token first.
func (s *State) Complete() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token = ""
}

func generateToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func logBanner(token string) {
	bar := strings.Repeat("=", 76)
	slog.Warn("admin setup required — visit /setup with the token below",
		"setup_token", token)
	// Also emit a human-friendly banner so it's unmissable in `docker logs`.
	fmt.Fprintln(os.Stdout,
		"\n"+bar+"\n"+
			"  PrivaSync — FIRST-RUN ADMIN SETUP\n"+
			"  Open  <your-server>/setup  and paste this token:\n"+
			"\n"+
			"    "+token+"\n"+
			"\n"+
			"  Token is valid only for this process; a restart regenerates it.\n"+
			bar)
}
