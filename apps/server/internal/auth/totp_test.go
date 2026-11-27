package auth

import (
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestGenerateTOTPSecret_ReturnsUsableSecret(t *testing.T) {
	secret, url, err := GenerateTOTPSecret("PrivaSync", "alice")
	if err != nil {
		t.Fatalf("GenerateTOTPSecret: %v", err)
	}
	if secret == "" {
		t.Fatal("secret must not be empty")
	}
	if url == "" {
		t.Fatal("otpauth URL must not be empty")
	}

	// The secret must be usable to generate a code that ValidateTOTPCode accepts.
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("totp.GenerateCode: %v", err)
	}
	if !ValidateTOTPCode(secret, code) {
		t.Fatal("freshly generated code must validate against its own secret")
	}
}

func TestValidateTOTPCode_InvalidCodeRejected(t *testing.T) {
	secret, _, err := GenerateTOTPSecret("PrivaSync", "bob")
	if err != nil {
		t.Fatalf("GenerateTOTPSecret: %v", err)
	}
	if ValidateTOTPCode(secret, "000000") && ValidateTOTPCode(secret, "111111") {
		// Astronomically unlikely both are the real current code; guards
		// against flakiness while still asserting garbage codes are rejected.
		t.Fatal("arbitrary codes must not both validate")
	}
	if ValidateTOTPCode(secret, "not-a-code") {
		t.Fatal("malformed code must be rejected")
	}
}

func TestValidateTOTPCode_MalformedSecretReturnsFalseNotPanic(t *testing.T) {
	if ValidateTOTPCode("not-valid-base32!!", "123456") {
		t.Fatal("malformed secret must not validate")
	}
}

func TestValidateTOTPCode_WrongSecretRejected(t *testing.T) {
	secretA, _, err := GenerateTOTPSecret("PrivaSync", "carol")
	if err != nil {
		t.Fatalf("GenerateTOTPSecret: %v", err)
	}
	secretB, _, err := GenerateTOTPSecret("PrivaSync", "dave")
	if err != nil {
		t.Fatalf("GenerateTOTPSecret: %v", err)
	}
	code, err := totp.GenerateCode(secretA, time.Now())
	if err != nil {
		t.Fatalf("totp.GenerateCode: %v", err)
	}
	if ValidateTOTPCode(secretB, code) {
		t.Fatal("code generated for secretA must not validate against secretB")
	}
}
