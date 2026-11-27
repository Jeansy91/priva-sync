package bootstrap

import "testing"

func TestState_ZeroValue_NeedsSetupFalse(t *testing.T) {
	var s State
	if s.NeedsSetup() {
		t.Fatal("zero-value State.NeedsSetup() = true, want false (no token set)")
	}
	if s.Verify("anything") {
		t.Fatal("zero-value State.Verify() must always be false")
	}
}

func TestState_ActiveToken_VerifyMatchAndMismatch(t *testing.T) {
	s := &State{token: "the-real-token"}

	if !s.NeedsSetup() {
		t.Fatal("NeedsSetup() = false, want true while a token is set")
	}
	if s.Verify("wrong-token") {
		t.Fatal("Verify() matched a wrong token")
	}
	if !s.Verify("the-real-token") {
		t.Fatal("Verify() rejected the correct token")
	}
}

func TestState_Complete_ClosesVerificationWindow(t *testing.T) {
	s := &State{token: "the-real-token"}
	s.Complete()

	if s.NeedsSetup() {
		t.Fatal("NeedsSetup() = true after Complete(), want false")
	}
	if s.Verify("the-real-token") {
		t.Fatal("Verify() accepted the correct token after Complete()")
	}
}

func TestGenerateToken_ReturnsNonEmptyUniqueValues(t *testing.T) {
	a, err := generateToken()
	if err != nil {
		t.Fatalf("generateToken: %v", err)
	}
	b, err := generateToken()
	if err != nil {
		t.Fatalf("generateToken: %v", err)
	}
	if a == "" || b == "" {
		t.Fatal("generateToken returned an empty string")
	}
	if a == b {
		t.Fatal("two calls to generateToken produced the same value")
	}
}
