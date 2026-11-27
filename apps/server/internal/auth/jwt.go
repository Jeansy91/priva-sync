package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"privasync/server/internal/model"
)

type TokenType string

const (
	TokenTypeAccess           TokenType = "access"
	TokenTypeRefresh          TokenType = "refresh"
	TokenType2FASession       TokenType = "2fa_session"
	TokenTypePasskeyChallenge TokenType = "passkey_challenge"
	// TokenTypeSSE authorises the event stream and nothing else. The browser's
	// EventSource cannot set an Authorization header, so this one travels in a
	// cookie; keeping it a distinct type means a leaked cookie cannot be
	// replayed against the rest of the API.
	TokenTypeSSE TokenType = "sse"
)

type Claims struct {
	jwt.RegisteredClaims
	UserID    uuid.UUID      `json:"uid"`
	Role      model.Role     `json:"role"`
	TokenType TokenType      `json:"type"`
	// PasskeyChallenge carries the JSON-marshaled webauthn.SessionData
	// between a passkey ceremony's begin and finish step. Only set for
	// TokenTypePasskeyChallenge. UserID is uuid.Nil for discoverable-login
	// challenges, where the user isn't known until the finish step.
	PasskeyChallenge string `json:"pkc,omitempty"`
}

var (
	ErrTokenExpired  = errors.New("token expired")
	ErrTokenInvalid  = errors.New("token invalid")
)

// IssueAccessToken erstellt ein kurzlebiges Access Token (JWT).
func IssueAccessToken(secret string, ttl time.Duration, userID uuid.UUID, role model.Role) (string, error) {
	return issueToken(secret, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ID:        uuid.New().String(),
		},
		UserID:    userID,
		Role:      role,
		TokenType: TokenTypeAccess,
	})
}

// IssueSSEToken creates a token that authorises only the event stream. It
// shares the access token's lifetime and is re-issued alongside it, so the
// cookie carrying it never outlives the session it belongs to.
func IssueSSEToken(secret string, ttl time.Duration, userID uuid.UUID, role model.Role) (string, error) {
	return issueToken(secret, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ID:        uuid.New().String(),
		},
		UserID:    userID,
		Role:      role,
		TokenType: TokenTypeSSE,
	})
}

// Issue2FASessionToken creates a short-lived token valid only for completing the 2FA flow.
func Issue2FASessionToken(secret string, ttl time.Duration, userID uuid.UUID) (string, error) {
	return issueToken(secret, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ID:        uuid.New().String(),
		},
		UserID:    userID,
		TokenType: TokenType2FASession,
	})
}

// IssuePasskeyChallengeToken creates a short-lived token carrying the
// WebAuthn session data between a ceremony's begin and finish step, mirroring
// Issue2FASessionToken's stateless hand-off idiom. userID is uuid.Nil for
// discoverable-login challenges (register challenges carry the caller's ID).
func IssuePasskeyChallengeToken(secret string, ttl time.Duration, userID uuid.UUID, sessionData string) (string, error) {
	return issueToken(secret, Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ID:        uuid.New().String(),
		},
		UserID:           userID,
		TokenType:        TokenTypePasskeyChallenge,
		PasskeyChallenge: sessionData,
	})
}

// ParseToken validates and parses a JWT. Returns the claims or an error.
func ParseToken(secret, tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, ErrTokenInvalid
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrTokenInvalid
	}
	return claims, nil
}

func issueToken(secret string, claims Claims) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("signing token: %w", err)
	}
	return signed, nil
}
