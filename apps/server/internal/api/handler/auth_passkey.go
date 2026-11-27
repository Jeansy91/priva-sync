package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"

	"privasync/server/internal/api/middleware"
	"privasync/server/internal/audit"
	"privasync/server/internal/auth"
	"privasync/server/internal/httpx"
	"privasync/server/internal/model"
	"privasync/server/internal/repository"
)

// errInvalidPasskeyChallenge is returned by parsePasskeyChallenge for any
// malformed/expired/wrong-type token — callers map it to ErrPasskeyChallengeInvalid.
var errInvalidPasskeyChallenge = errors.New("invalid passkey challenge token")

// issuePasskeyChallenge marshals WebAuthn session data into a short-lived
// challenge token, mirroring Issue2FASessionToken's hand-off idiom (see
// auth.IssuePasskeyChallengeToken). userID is nil for discoverable-login
// challenges, where the user isn't known until the finish step.
func (h *AuthHandler) issuePasskeyChallenge(userID *uuid.UUID, session *webauthn.SessionData) (string, error) {
	data, err := json.Marshal(session)
	if err != nil {
		return "", err
	}
	id := uuid.Nil
	if userID != nil {
		id = *userID
	}
	return auth.IssuePasskeyChallengeToken(h.cfg.JWTSecret, h.cfg.PasskeyChallengeTTL, id, string(data))
}

func (h *AuthHandler) parsePasskeyChallenge(token string) (*webauthn.SessionData, *uuid.UUID, error) {
	claims, err := auth.ParseToken(h.cfg.JWTSecret, token)
	if err != nil || claims.TokenType != auth.TokenTypePasskeyChallenge {
		return nil, nil, errInvalidPasskeyChallenge
	}
	var session webauthn.SessionData
	if err := json.Unmarshal([]byte(claims.PasskeyChallenge), &session); err != nil {
		return nil, nil, errInvalidPasskeyChallenge
	}
	var userID *uuid.UUID
	if claims.UserID != uuid.Nil {
		id := claims.UserID
		userID = &id
	}
	return &session, userID, nil
}

// userCredentials loads and decodes a user's stored WebAuthn credentials for
// the library's exclusion list (register) or credential lookup (login).
func (h *AuthHandler) userCredentials(ctx context.Context, userID uuid.UUID) ([]webauthn.Credential, error) {
	rows, err := h.passkeys.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	creds := make([]webauthn.Credential, 0, len(rows))
	for _, row := range rows {
		var c webauthn.Credential
		if err := json.Unmarshal(row.Credential, &c); err != nil {
			return nil, err
		}
		creds = append(creds, c)
	}
	return creds, nil
}

// POST /api/v1/auth/passkey/register/begin (protected)
func (h *AuthHandler) RegisterPasskeyBegin(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	user, err := h.users.FindByID(r.Context(), claims.UserID)
	if err != nil {
		writeError(w, http.StatusNotFound, ErrUserNotFound, "user not found")
		return
	}

	existing, err := h.userCredentials(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	waUser := auth.NewWebAuthnUser(user, existing)
	creation, session, err := h.webAuthn.BeginRegistration(waUser,
		webauthn.WithExclusions(webauthn.Credentials(existing).CredentialDescriptors()),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	challengeToken, err := h.issuePasskeyChallenge(&user.ID, session)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"options":         creation,
		"challenge_token": challengeToken,
	})
}

// POST /api/v1/auth/passkey/register/finish (protected)
func (h *AuthHandler) RegisterPasskeyFinish(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	user, err := h.users.FindByID(r.Context(), claims.UserID)
	if err != nil {
		writeError(w, http.StatusNotFound, ErrUserNotFound, "user not found")
		return
	}

	var req struct {
		ChallengeToken string          `json:"challenge_token"`
		Credential     json.RawMessage `json:"credential"`
		Name           string          `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ChallengeToken == "" || len(req.Credential) == 0 {
		writeError(w, http.StatusBadRequest, ErrInvalidBody, "invalid request body")
		return
	}

	session, challengeUserID, err := h.parsePasskeyChallenge(req.ChallengeToken)
	if err != nil || challengeUserID == nil || *challengeUserID != user.ID {
		writeError(w, http.StatusUnauthorized, ErrPasskeyChallengeInvalid, "invalid or expired challenge")
		return
	}

	parsedResponse, err := protocol.ParseCredentialCreationResponseBody(bytes.NewReader(req.Credential))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrPasskeyVerificationFailed, "invalid credential response")
		return
	}

	waUser := auth.NewWebAuthnUser(user, nil)
	credential, err := h.webAuthn.CreateCredential(waUser, *session, parsedResponse)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrPasskeyVerificationFailed, "credential verification failed")
		return
	}

	credJSON, err := json.Marshal(credential)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	name := req.Name
	if name == "" {
		name = "Passkey"
	}

	stored, err := h.passkeys.Create(r.Context(), user.ID, credential.ID, credJSON, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	_ = h.audits.Log(r.Context(), audit.FromRequest(r, &user.ID, audit.EventPasskeyRegistered, map[string]any{"name": name}))

	writeJSON(w, http.StatusCreated, stored)
}

// POST /api/v1/auth/passkey/login/begin (public)
func (h *AuthHandler) PasskeyLoginBegin(w http.ResponseWriter, r *http.Request) {
	assertion, session, err := h.webAuthn.BeginDiscoverableLogin()
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	challengeToken, err := h.issuePasskeyChallenge(nil, session)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"options":         assertion,
		"challenge_token": challengeToken,
	})
}

// POST /api/v1/auth/passkey/login/finish (public)
func (h *AuthHandler) PasskeyLoginFinish(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChallengeToken string          `json:"challenge_token"`
		Credential     json.RawMessage `json:"credential"`
		Client         *clientPayload  `json:"client"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ChallengeToken == "" || len(req.Credential) == 0 {
		writeError(w, http.StatusBadRequest, ErrInvalidBody, "invalid request body")
		return
	}

	session, _, err := h.parsePasskeyChallenge(req.ChallengeToken)
	if err != nil {
		writeError(w, http.StatusUnauthorized, ErrPasskeyChallengeInvalid, "invalid or expired challenge")
		return
	}

	parsedResponse, err := protocol.ParseCredentialRequestResponseBody(bytes.NewReader(req.Credential))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrPasskeyVerificationFailed, "invalid credential response")
		return
	}

	ip := httpx.ClientIP(r)

	var matchedUser *model.User
	lookup := func(rawID, userHandle []byte) (webauthn.User, error) {
		ownerID, err := h.passkeys.FindByCredentialID(r.Context(), rawID)
		if err != nil {
			return nil, err
		}
		u, err := h.users.FindByID(r.Context(), ownerID)
		if err != nil {
			return nil, err
		}
		creds, err := h.userCredentials(r.Context(), u.ID)
		if err != nil {
			return nil, err
		}
		matchedUser = u
		return auth.NewWebAuthnUser(u, creds), nil
	}

	_, credential, err := h.webAuthn.ValidatePasskeyLogin(lookup, *session, parsedResponse)
	if err != nil || matchedUser == nil {
		_ = h.audits.Log(r.Context(), audit.FromRequest(r, nil, audit.EventLoginFail, map[string]any{"reason": "passkey_verification_failed"}))
		writeError(w, http.StatusUnauthorized, ErrPasskeyVerificationFailed, "passkey verification failed")
		return
	}

	if !matchedUser.IsActive {
		writeError(w, http.StatusForbidden, ErrAccountDisabled, "account disabled")
		return
	}

	// Sign-count / last-used update is defense-in-depth (clone detection),
	// not a login gate — a failure here is logged but never blocks the
	// login, same as upsertClient below.
	if credJSON, err := json.Marshal(credential); err != nil {
		slog.Warn("passkey credential marshal failed", "user_id", matchedUser.ID, "error", err)
	} else if err := h.passkeys.UpdateSignCount(r.Context(), credential.ID, credJSON); err != nil {
		slog.Warn("passkey sign count update failed", "user_id", matchedUser.ID, "error", err)
	}

	_ = h.users.RecordLoginAttempt(r.Context(), matchedUser.Username, ip, true)
	_ = h.audits.Log(r.Context(), audit.FromRequest(r, &matchedUser.ID, audit.EventPasskeyLogin, nil))

	h.upsertClient(r, matchedUser.ID, req.Client, ip)
	h.issueTokenPair(w, r, matchedUser.ID, matchedUser.Role)
}

// GET /api/v1/auth/passkey (protected)
func (h *AuthHandler) ListPasskeys(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	rows, err := h.passkeys.ListByUser(r.Context(), claims.UserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// DELETE /api/v1/auth/passkey/{passkeyID} (protected)
func (h *AuthHandler) DeletePasskey(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFromContext(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "passkeyID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrInvalidPath, "invalid passkey id")
		return
	}

	if err := h.passkeys.Delete(r.Context(), id, claims.UserID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, ErrPasskeyNotFound, "passkey not found")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrInternal, "internal error")
		return
	}

	_ = h.audits.Log(r.Context(), audit.FromRequest(r, &claims.UserID, audit.EventPasskeyRemoved, nil))
	w.WriteHeader(http.StatusNoContent)
}
