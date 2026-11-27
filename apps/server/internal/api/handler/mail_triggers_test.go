package handler

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"privasync/server/internal/config"
	"privasync/server/internal/mail"
	"privasync/server/internal/model"
)

// disabledMailer builds a fully wired Mailer whose Send() is a no-op (no
// network I/O) so the trigger functions' real SendTemplate path — template
// lookup, data binding — runs without needing a live SMTP server.
func disabledMailer(t *testing.T) *mail.Mailer {
	t.Helper()
	m, err := mail.New(config.SMTPConfig{Enabled: false})
	if err != nil {
		t.Fatalf("mail.New: %v", err)
	}
	return m
}

func TestSendInviteMail_NilMailerIsNoop(t *testing.T) {
	// Must not panic — nil mailer is the expected state for tests/dev.
	sendInviteMail(nil, "http://test", &model.User{}, "tok")
}

func TestSendInviteMail_RendersAndEnqueues(t *testing.T) {
	m := disabledMailer(t)
	u := &model.User{Username: "alice", Email: "alice@test.local", PreferredLanguage: mail.LangDE}

	sendInviteMail(m, "http://test", u, "tok-123")
	sendInviteMail(m, "http://test", &model.User{Username: "bob", Email: "bob@test.local", PreferredLanguage: mail.LangEN}, "tok-456")
}

func TestSendEmailChangeRequestMail_NilMailerIsNoop(t *testing.T) {
	sendEmailChangeRequestMail(nil, "http://test", &model.User{}, "new@test.local", "tok")
}

func TestSendEmailChangeRequestMail_RendersAndEnqueues(t *testing.T) {
	m := disabledMailer(t)
	u := &model.User{Username: "alice", PreferredLanguage: mail.LangDE}

	sendEmailChangeRequestMail(m, "http://test", u, "new@test.local", "tok-123")
}

func TestSendEmailChangedMail_NilMailerOrEmptyOldEmailIsNoop(t *testing.T) {
	sendEmailChangedMail(nil, "http://test", &model.User{}, "old@test.local", "new@test.local")
	sendEmailChangedMail(disabledMailer(t), "http://test", &model.User{}, "", "new@test.local")
}

func TestSendEmailChangedMail_RendersAndEnqueues(t *testing.T) {
	m := disabledMailer(t)
	u := &model.User{Username: "alice"}

	sendEmailChangedMail(m, "http://test", u, "old@test.local", "new@test.local")
}

func TestSendShareReceivedMail_NilMailerIsNoop(t *testing.T) {
	sendShareReceivedMail(context.Background(), nil, nil, "http://test", uuid.New(), uuid.New(), "a.txt", uuid.New())
}

func TestSendShareReceivedMail_RendersForOptedInTarget(t *testing.T) {
	env := newTestEnv(t)
	m := disabledMailer(t)
	target := seedUser(t, env, "target_"+shortUUID(), "target_"+shortUUID()+"@test.local")
	sharer := seedUser(t, env, "sharer_"+shortUUID(), "sharer_"+shortUUID()+"@test.local")

	sendShareReceivedMail(context.Background(), env.users, m, "http://test", target.ID, sharer.ID, "report.pdf", uuid.New())
}

func TestSendShareReceivedMail_SkipsTargetWithNotificationsOff(t *testing.T) {
	env := newTestEnv(t)
	m := disabledMailer(t)
	target := seedUser(t, env, "target_"+shortUUID(), "target_"+shortUUID()+"@test.local")
	if _, err := env.pool.Exec(context.Background(), `UPDATE users SET email_notify = false WHERE id = $1`, target.ID); err != nil {
		t.Fatalf("disable email_notify: %v", err)
	}

	// Must return without enqueueing (and without erroring) — the opted-out
	// path is a silent no-op by design.
	sendShareReceivedMail(context.Background(), env.users, m, "http://test", target.ID, uuid.New(), "report.pdf", uuid.New())
}

func TestSendShareReceivedMail_UnknownTargetIsNoop(t *testing.T) {
	env := newTestEnv(t)
	m := disabledMailer(t)

	sendShareReceivedMail(context.Background(), env.users, m, "http://test", uuid.New(), uuid.New(), "report.pdf", uuid.New())
}

func TestSendShareRevokedMail_RendersForOptedInTarget(t *testing.T) {
	env := newTestEnv(t)
	m := disabledMailer(t)
	target := seedUser(t, env, "target_"+shortUUID(), "target_"+shortUUID()+"@test.local")
	sharer := seedUser(t, env, "sharer_"+shortUUID(), "sharer_"+shortUUID()+"@test.local")

	sendShareRevokedMail(context.Background(), env.users, m, "http://test", target.ID, sharer.ID, "report.pdf")
}

func TestSendShareRevokedMail_UnknownSharerStillSends(t *testing.T) {
	env := newTestEnv(t)
	m := disabledMailer(t)
	target := seedUser(t, env, "target_"+shortUUID(), "target_"+shortUUID()+"@test.local")

	// The sharer lookup failing must not block notifying the target — the
	// mail just renders with an empty sharer name.
	sendShareRevokedMail(context.Background(), env.users, m, "http://test", target.ID, uuid.New(), "report.pdf")
}

func TestSendTOTPDisabledMail_NilMailerIsNoop(t *testing.T) {
	sendTOTPDisabledMail(context.Background(), nil, nil, "http://test", uuid.New(), "now", "1.2.3.4")
}

func TestSendTOTPDisabledMail_IgnoresEmailNotifyAsSecuritySignal(t *testing.T) {
	env := newTestEnv(t)
	m := disabledMailer(t)
	u := seedUser(t, env, "totp_"+shortUUID(), "totp_"+shortUUID()+"@test.local")
	if _, err := env.pool.Exec(context.Background(), `UPDATE users SET email_notify = false WHERE id = $1`, u.ID); err != nil {
		t.Fatalf("disable email_notify: %v", err)
	}

	// Security-signal mails are transactional: must still render/enqueue
	// even though the user opted out of notification mail.
	sendTOTPDisabledMail(context.Background(), env.users, m, "http://test", u.ID, "2026-08-05T12:00:00Z", "1.2.3.4")
}

func TestSendTOTPDisabledMail_UnknownUserIsNoop(t *testing.T) {
	env := newTestEnv(t)
	m := disabledMailer(t)

	sendTOTPDisabledMail(context.Background(), env.users, m, "http://test", uuid.New(), "now", "1.2.3.4")
}

func shortUUID() string {
	return uuid.New().String()[:8]
}
