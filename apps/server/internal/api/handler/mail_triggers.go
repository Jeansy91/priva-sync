package handler

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"privasync/server/internal/mail"
	"privasync/server/internal/model"
	"privasync/server/internal/repository"
)

// sendInviteMail enqueues the welcome/invite mail after admin.CreateUser.
// The mail carries a one-time link the user redeems to set their own
// password — no credential ever travels in plaintext.
func sendInviteMail(
	mailer *mail.Mailer,
	baseURL string,
	user *model.User,
	token string,
) {
	if mailer == nil {
		return
	}
	inviteURL := baseURL + "/password-reset?token=" + token
	expiresIn := "72 hours"
	if user.PreferredLanguage == mail.LangDE {
		expiresIn = "72 Stunden"
	}
	if err := mailer.SendTemplate(user.Email, user.PreferredLanguage, mail.TplWelcome, mail.WelcomeData{
		Common:    mail.Common{BaseURL: baseURL, Username: user.Username, OptIn: false},
		Email:     user.Email,
		InviteURL: inviteURL,
		ExpiresIn: expiresIn,
	}); err != nil {
		slog.Warn("invite: mail enqueue failed", "user_id", user.ID, "error", err)
	}
}

// sendShareReceivedMail notifies the target user about an incoming user-share.
// Respects email_notify because shares are in the opt-out category.
func sendShareReceivedMail(
	ctx context.Context,
	users *repository.UserRepository,
	mailer *mail.Mailer,
	baseURL string,
	targetID uuid.UUID,
	sharerID uuid.UUID,
	fileName string,
	shareID uuid.UUID,
) {
	if mailer == nil {
		return
	}
	target, err := users.FindByID(ctx, targetID)
	if err != nil || !target.EmailNotify {
		return
	}
	sharerName := ""
	if sharer, err := users.FindByID(ctx, sharerID); err == nil {
		sharerName = sharer.Username
	}
	if err := mailer.SendTemplate(target.Email, target.PreferredLanguage, mail.TplShareReceived, mail.ShareReceivedData{
		Common:     mail.Common{BaseURL: baseURL, Username: target.Username, OptIn: true},
		SharerName: sharerName,
		FileName:   fileName,
		ShareURL:   baseURL + "/shares/" + shareID.String(),
	}); err != nil {
		slog.Warn("share received: mail enqueue failed", "target_id", targetID, "error", err)
	}
}

// sendShareRevokedMail informs the target user when the giver explicitly
// revoked a user-share. Respects email_notify because share lifecycle is
// an opt-out category. See ADR-0010.
func sendShareRevokedMail(
	ctx context.Context,
	users *repository.UserRepository,
	mailer *mail.Mailer,
	baseURL string,
	targetID uuid.UUID,
	sharerID uuid.UUID,
	fileName string,
) {
	if mailer == nil {
		return
	}
	target, err := users.FindByID(ctx, targetID)
	if err != nil || !target.EmailNotify {
		return
	}
	sharerName := ""
	if sharer, err := users.FindByID(ctx, sharerID); err == nil {
		sharerName = sharer.Username
	}
	if err := mailer.SendTemplate(target.Email, target.PreferredLanguage, mail.TplShareRevoked, mail.ShareRevokedData{
		Common:     mail.Common{BaseURL: baseURL, Username: target.Username, OptIn: true},
		SharerName: sharerName,
		FileName:   fileName,
	}); err != nil {
		slog.Warn("share revoked: mail enqueue failed", "target_id", targetID, "error", err)
	}
}

// sendEmailChangeRequestMail schickt den Bestaetigungslink an die neue
// Adresse. mailer-nil ist okay (Tests).
func sendEmailChangeRequestMail(
	mailer *mail.Mailer,
	baseURL string,
	user *model.User,
	newEmail, token string,
) {
	if mailer == nil {
		return
	}
	confirmURL := baseURL + "/email-change/confirm?token=" + token
	expiresIn := "24 hours"
	if user.PreferredLanguage == mail.LangDE {
		expiresIn = "24 Stunden"
	}
	if err := mailer.SendTemplate(newEmail, user.PreferredLanguage, mail.TplEmailChangeRequest, mail.EmailChangeRequestData{
		Common:     mail.Common{BaseURL: baseURL, Username: user.Username, OptIn: false},
		NewEmail:   newEmail,
		ConfirmURL: confirmURL,
		ExpiresIn:  expiresIn,
	}); err != nil {
		slog.Warn("email change request: mail enqueue failed", "user_id", user.ID, "error", err)
	}
}

// sendEmailChangedMail informiert die *alte* Adresse, dass die Mail
// umgezogen ist — Security-Signal wie bei TOTP-Disable.
func sendEmailChangedMail(
	mailer *mail.Mailer,
	baseURL string,
	user *model.User,
	oldEmail, newEmail string,
) {
	if mailer == nil || oldEmail == "" {
		return
	}
	if err := mailer.SendTemplate(oldEmail, user.PreferredLanguage, mail.TplEmailChanged, mail.EmailChangedData{
		Common:   mail.Common{BaseURL: baseURL, Username: user.Username, OptIn: false},
		OldEmail: oldEmail,
		NewEmail: newEmail,
	}); err != nil {
		slog.Warn("email changed: mail enqueue failed", "user_id", user.ID, "error", err)
	}
}

// sendTOTPDisabledMail fires after auth.DisableTOTP as a security signal.
// Always sent — treated as transactional, not marketing — so email_notify
// is ignored.
func sendTOTPDisabledMail(
	ctx context.Context,
	users authUserStore,
	mailer *mail.Mailer,
	baseURL string,
	userID uuid.UUID,
	when string,
	ip string,
) {
	if mailer == nil {
		return
	}
	u, err := users.FindByID(ctx, userID)
	if err != nil {
		return
	}
	if err := mailer.SendTemplate(u.Email, u.PreferredLanguage, mail.TplTOTPDisabled, mail.TOTPDisabledData{
		Common:    mail.Common{BaseURL: baseURL, Username: u.Username, OptIn: false},
		When:      when,
		IPAddress: ip,
	}); err != nil {
		slog.Warn("totp disabled: mail enqueue failed", "user_id", userID, "error", err)
	}
}
