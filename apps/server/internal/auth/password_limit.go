package auth

import (
	"runtime"
	"sync/atomic"
)

// Argon2id is memory-hard on purpose: argonMemory (64 MiB) is committed for
// the duration of every hash. The cost is paid before the password is known
// to be wrong, so an unauthenticated caller decides how much memory the
// process holds simply by opening connections -- the WebDAV listener takes
// Basic Auth with no rate limit at all.
//
// The semaphore turns that into a constant: at most maxHashConcurrency hashes
// run at once, so peak hashing memory is bounded by the cap rather than by
// the traffic. Callers queue instead of failing; the request timeout is what
// sheds load beyond that.
//
// Half of GOMAXPROCS, because argon2.IDKey itself fans out over argonThreads
// workers -- running one hash per core would oversubscribe the CPU and make
// every concurrent verification slower than serialising them.
var (
	hashSem      = make(chan struct{}, maxHashConcurrency())
	hashInFlight atomic.Int32
	hashPeak     atomic.Int32
)

func maxHashConcurrency() int {
	if n := runtime.GOMAXPROCS(0) / 2; n > 1 {
		return n
	}
	return 1
}

// MaxHashConcurrency reports how many password hashes may run at once.
func MaxHashConcurrency() int { return cap(hashSem) }

// HashPeakConcurrency reports the highest number of hashes seen running
// simultaneously since the process started. Useful to tell "the cap is
// generous" apart from "the cap is the bottleneck" when tuning it.
func HashPeakConcurrency() int { return int(hashPeak.Load()) }

// withHashSlot runs fn while holding one of the hashing slots.
func withHashSlot[T any](fn func() T) T {
	hashSem <- struct{}{}
	if n := hashInFlight.Add(1); n > hashPeak.Load() {
		hashPeak.Store(n)
	}
	defer func() {
		hashInFlight.Add(-1)
		<-hashSem
	}()
	return fn()
}
