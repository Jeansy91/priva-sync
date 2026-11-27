package auth

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/model"
)

const testSecret = "test-jwt-secret"

func TestIssueAccessToken_ParseRoundTrip(t *testing.T) {
	userID := uuid.New()
	tok, err := IssueAccessToken(testSecret, time.Hour, userID, model.RoleUser)
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}

	claims, err := ParseToken(testSecret, tok)
	if err != nil {
		t.Fatalf("ParseToken: %v", err)
	}
	if claims.UserID != userID {
		t.Errorf("UserID = %v, want %v", claims.UserID, userID)
	}
	if claims.Role != model.RoleUser {
		t.Errorf("Role = %v, want %v", claims.Role, model.RoleUser)
	}
	if claims.TokenType != TokenTypeAccess {
		t.Errorf("TokenType = %v, want %v", claims.TokenType, TokenTypeAccess)
	}
}

func TestParseToken_WrongSecretRejected(t *testing.T) {
	tok, err := IssueAccessToken(testSecret, time.Hour, uuid.New(), model.RoleUser)
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	if _, err := ParseToken("a-different-secret", tok); err != ErrTokenInvalid {
		t.Fatalf("ParseToken: got %v, want ErrTokenInvalid", err)
	}
}

func TestParseToken_ExpiredTokenRejected(t *testing.T) {
	tok, err := IssueAccessToken(testSecret, -time.Minute, uuid.New(), model.RoleUser)
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	if _, err := ParseToken(testSecret, tok); err != ErrTokenExpired {
		t.Fatalf("ParseToken: got %v, want ErrTokenExpired", err)
	}
}

func TestParseToken_TamperedTokenRejected(t *testing.T) {
	tok, err := IssueAccessToken(testSecret, time.Hour, uuid.New(), model.RoleUser)
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	tampered := tok[:len(tok)-2] + "xx"
	if _, err := ParseToken(testSecret, tampered); err != ErrTokenInvalid {
		t.Fatalf("ParseToken: got %v, want ErrTokenInvalid", err)
	}
}

func TestParseToken_GarbageRejected(t *testing.T) {
	if _, err := ParseToken(testSecret, "not-a-jwt-at-all"); err != ErrTokenInvalid {
		t.Fatalf("ParseToken: got %v, want ErrTokenInvalid", err)
	}
}

func TestIssue2FASessionToken_HasCorrectType(t *testing.T) {
	userID := uuid.New()
	tok, err := Issue2FASessionToken(testSecret, 5*time.Minute, userID)
	if err != nil {
		t.Fatalf("Issue2FASessionToken: %v", err)
	}
	claims, err := ParseToken(testSecret, tok)
	if err != nil {
		t.Fatalf("ParseToken: %v", err)
	}
	if claims.TokenType != TokenType2FASession {
		t.Errorf("TokenType = %v, want %v", claims.TokenType, TokenType2FASession)
	}
	if claims.UserID != userID {
		t.Errorf("UserID = %v, want %v", claims.UserID, userID)
	}
}

func TestIssuePasskeyChallengeToken_CarriesSessionData(t *testing.T) {
	sessionData := `{"challenge":"abc"}`
	tok, err := IssuePasskeyChallengeToken(testSecret, 5*time.Minute, uuid.Nil, sessionData)
	if err != nil {
		t.Fatalf("IssuePasskeyChallengeToken: %v", err)
	}
	claims, err := ParseToken(testSecret, tok)
	if err != nil {
		t.Fatalf("ParseToken: %v", err)
	}
	if claims.TokenType != TokenTypePasskeyChallenge {
		t.Errorf("TokenType = %v, want %v", claims.TokenType, TokenTypePasskeyChallenge)
	}
	if claims.PasskeyChallenge != sessionData {
		t.Errorf("PasskeyChallenge = %q, want %q", claims.PasskeyChallenge, sessionData)
	}
	if claims.UserID != uuid.Nil {
		t.Errorf("UserID = %v, want uuid.Nil", claims.UserID)
	}
}
