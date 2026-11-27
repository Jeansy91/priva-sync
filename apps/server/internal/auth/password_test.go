package auth

import "testing"

func TestHashPassword_VerifyRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := VerifyPassword("correct-horse-battery-staple", hash); err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
}

func TestVerifyPassword_WrongPasswordRejected(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := VerifyPassword("wrong-password", hash); err != ErrPasswordMismatch {
		t.Fatalf("VerifyPassword: got %v, want ErrPasswordMismatch", err)
	}
}

func TestVerifyPassword_InvalidHashFormatRejected(t *testing.T) {
	cases := []string{
		"",
		"not-a-hash",
		"$argon2id$v=19$m=65536,t=2,p=4$onlyfiveparts",
	}
	for _, encoded := range cases {
		if err := VerifyPassword("anything", encoded); err != ErrInvalidHash {
			t.Errorf("VerifyPassword(%q): got %v, want ErrInvalidHash", encoded, err)
		}
	}
}

func TestHashPassword_UniqueSaltPerCall(t *testing.T) {
	h1, err := HashPassword("same-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	h2, err := HashPassword("same-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if h1 == h2 {
		t.Fatal("two hashes of the same password must differ (random salt)")
	}
	// Both must still verify independently.
	if err := VerifyPassword("same-password", h1); err != nil {
		t.Errorf("VerifyPassword(h1): %v", err)
	}
	if err := VerifyPassword("same-password", h2); err != nil {
		t.Errorf("VerifyPassword(h2): %v", err)
	}
}
