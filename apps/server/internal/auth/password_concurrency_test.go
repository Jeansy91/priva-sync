package auth

import (
	"sync"
	"testing"
)

// resetHashPeak clears the high-water mark so one scenario does not inherit
// an earlier one's measurement.
func resetHashPeak() { hashPeak.Store(0) }

// Argon2id is memory-hard by design: every verification allocates 64 MiB
// while it runs. That cost is paid *before* the password is known to be
// wrong, so an unauthenticated caller controls how much memory the process
// commits simply by opening connections. Without a ceiling the parameter that
// makes hashing safe is also what makes the server easy to knock over.
func TestVerifyPassword_ConcurrencyIsBounded(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	limit := MaxHashConcurrency()
	if limit < 1 {
		t.Fatalf("MaxHashConcurrency() = %d, want at least 1", limit)
	}
	resetHashPeak()

	var wg sync.WaitGroup
	for i := 0; i < limit*4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = VerifyPassword("wrong-password", hash)
		}()
	}
	wg.Wait()

	peak := HashPeakConcurrency()
	if peak > limit {
		t.Fatalf("peak concurrent hashes = %d, want at most %d — the cap does not hold", peak, limit)
	}
	// Guard against a vacuous pass: with limit*4 goroutines the slots must
	// actually have been contended, otherwise the assertion above proves
	// nothing about the cap.
	if limit > 1 && peak < 2 {
		t.Fatalf("peak concurrent hashes = %d with %d goroutines — the test never contended the cap",
			peak, limit*4)
	}
}

// The cap must not change the outcome of a verification, only its pacing.
func TestVerifyPassword_StillCorrectUnderContention(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	var wg sync.WaitGroup
	errs := make([]error, MaxHashConcurrency()*3)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = VerifyPassword("correct-horse-battery-staple", hash)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: VerifyPassword returned %v for the correct password", i, err)
		}
	}
}
