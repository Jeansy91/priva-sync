package assembler_test

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"privasync/server/internal/assembler"
)

// A submitted session must actually reach the runner — everything else the
// pool does is a restriction on top of this.
func TestPool_RunsSubmittedWork(t *testing.T) {
	ran := make(chan assembler.Job, 1)
	pool := assembler.New(assembler.Config{
		Workers: 1, PerUser: 1,
		Run: func(j assembler.Job) bool { ran <- j; return false },
	})
	pool.Start()
	defer pool.Stop()

	job := assembler.Job{SessionID: uuid.New(), UserID: uuid.New()}
	pool.Submit(job)

	select {
	case got := <-ran:
		if got != job {
			t.Fatalf("ran %v, want %v", got, job)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("submitted job never ran")
	}
}

// Two large uploads from the same user must not both occupy the disk: the
// second waits, however many workers are free.
func TestPool_HoldsBackASecondJobForTheSameUser(t *testing.T) {
	user := uuid.New()
	started := make(chan uuid.UUID, 2)
	release := make(chan struct{})

	pool := assembler.New(assembler.Config{
		Workers: 2, PerUser: 1,
		Run: func(j assembler.Job) bool {
			started <- j.SessionID
			<-release
			return false
		},
	})
	pool.Start()
	defer func() { close(release); pool.Stop() }()

	pool.Submit(assembler.Job{SessionID: uuid.New(), UserID: user})
	pool.Submit(assembler.Job{SessionID: uuid.New(), UserID: user})

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first job never started")
	}
	select {
	case <-started:
		t.Fatal("second job for the same user started while the first was running")
	case <-time.After(150 * time.Millisecond):
	}
}

// The per-user limit is a fairness rule, not a global one — two users assemble
// side by side as long as there are workers.
func TestPool_RunsTwoUsersSideBySide(t *testing.T) {
	both := make(chan struct{}, 2)
	release := make(chan struct{})

	pool := assembler.New(assembler.Config{
		Workers: 2, PerUser: 1,
		Run: func(assembler.Job) bool {
			both <- struct{}{}
			<-release
			return false
		},
	})
	pool.Start()
	defer func() { close(release); pool.Stop() }()

	pool.Submit(assembler.Job{SessionID: uuid.New(), UserID: uuid.New()})
	pool.Submit(assembler.Job{SessionID: uuid.New(), UserID: uuid.New()})

	for i := 0; i < 2; i++ {
		select {
		case <-both:
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d of 2 jobs started", i)
		}
	}
}

// A client retrying its complete, plus a boot sweep, can name the same session
// twice. Running it twice would mean two staging files and two commits.
func TestPool_IgnoresASessionItIsAlreadyHolding(t *testing.T) {
	release := make(chan struct{})
	var runs int32

	pool := assembler.New(assembler.Config{
		Workers: 2, PerUser: 2,
		Run: func(assembler.Job) bool {
			atomic.AddInt32(&runs, 1)
			<-release
			return false
		},
	})
	pool.Start()
	defer func() { close(release); pool.Stop() }()

	job := assembler.Job{SessionID: uuid.New(), UserID: uuid.New()}
	if !pool.Submit(job) {
		t.Fatal("first Submit was refused")
	}
	if pool.Submit(job) {
		t.Fatal("duplicate Submit was accepted")
	}

	deadline := time.After(2 * time.Second)
	for atomic.LoadInt32(&runs) < 1 {
		select {
		case <-deadline:
			t.Fatal("job never ran")
		default:
		}
	}
	if got := atomic.LoadInt32(&runs); got != 1 {
		t.Fatalf("runs = %d, want 1", got)
	}
}

// A transient failure asks for another attempt; the pool is what grants it.
func TestPool_GrantsAnotherAttemptWhenTheRunAsksForOne(t *testing.T) {
	attempts := make(chan int, 4)
	var n int32

	pool := assembler.New(assembler.Config{
		Workers: 1, PerUser: 1,
		Run: func(assembler.Job) bool {
			cur := int(atomic.AddInt32(&n, 1))
			attempts <- cur
			return cur == 1 // first attempt wants a retry, second is done
		},
	})
	pool.Start()
	defer pool.Stop()

	pool.Submit(assembler.Job{SessionID: uuid.New(), UserID: uuid.New()})

	for want := 1; want <= 2; want++ {
		select {
		case got := <-attempts:
			if got != want {
				t.Fatalf("attempt %d, want %d", got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("attempt %d never happened", want)
		}
	}
}
