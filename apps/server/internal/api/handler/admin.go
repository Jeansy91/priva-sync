package handler

import (
	"context"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/audit"
	"privasync/server/internal/config"
	"privasync/server/internal/mail"
	"privasync/server/internal/model"
)

// adminUserStore is the slice of *repository.UserRepository that AdminHandler
// actually calls (ADR-0029). Narrow by design so the concrete repo satisfies
// it structurally and error-path fakes stay tiny.
type adminUserStore interface {
	List(ctx context.Context) ([]model.User, error)
	FailedAttemptsByUser(ctx context.Context, window time.Duration) (map[string]int, error)
	Create(ctx context.Context, username, email, passwordHash string, role model.Role) (*model.User, error)
	CreatePasswordResetToken(ctx context.Context, userID uuid.UUID, token string, expiresAt time.Time) error
	SetQuota(ctx context.Context, userID uuid.UUID, quotaBytes int64) error
	SetRole(ctx context.Context, userID uuid.UUID, role model.Role) error
	FindByID(ctx context.Context, id uuid.UUID) (*model.User, error)
	ClearFailedAttempts(ctx context.Context, username string) error
	SetActive(ctx context.Context, userID uuid.UUID, active bool) error
}

// adminAuditStore is the audit surface AdminHandler needs (ADR-0029).
type adminAuditStore interface {
	Log(ctx context.Context, e audit.Event) error
	List(ctx context.Context, f audit.ListFilter) ([]audit.Event, error)
}

// adminQuotaService is the quota surface AdminHandler needs (ADR-0029).
type adminQuotaService interface {
	RecomputeViaBus(ctx context.Context, userID uuid.UUID) error
}

type AdminHandler struct {
	users   adminUserStore
	audits  adminAuditStore
	mailer  *mail.Mailer
	quota   adminQuotaService
	cfg     config.AuthConfig
	baseURL string
	// newInviteToken generates the one-time invite token mailed to a fresh
	// user. A field (not a call to rand directly) so tests can pin the token
	// and redeem it through the public reset-confirm endpoint.
	newInviteToken func() (string, error)
}

func NewAdminHandler(users adminUserStore, audits adminAuditStore, mailer *mail.Mailer, q adminQuotaService, cfg config.AuthConfig, baseURL string) *AdminHandler {
	return &AdminHandler{
		users: users, audits: audits, mailer: mailer, quota: q, cfg: cfg, baseURL: baseURL,
		newInviteToken: newSecureToken,
	}
}
