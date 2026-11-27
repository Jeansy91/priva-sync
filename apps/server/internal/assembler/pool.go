// Package assembler turns a completed chunked upload into a committed file.
// The work is minutes of disk I/O for a multi-gigabyte upload, far longer than
// any request may live, so it runs here instead of in the handler
// (ADR-0043).
package assembler

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// Job identifies one unit of work: the session to assemble, and the user it
// belongs to — which is what the per-user limit counts.
type Job struct {
	SessionID uuid.UUID
	UserID    uuid.UUID
}

// Config describes the scheduling policy. Run does the actual work and reports
// whether the job deserves another attempt; the pool decides when.
type Config struct {
	Workers    int
	PerUser    int
	RetryDelay time.Duration
	Run        func(Job) bool
}

// Pool schedules assembly jobs across a small number of workers. Small on
// purpose: assembly is bound by the data volume, so more workers only make the
// same disk slower while multiplying the peak staging space in use.
type Pool struct {
	cfg Config

	mu      sync.Mutex
	cond    *sync.Cond
	queue   []Job
	pending map[uuid.UUID]struct{} // sessions queued or running, for dedupe
	running map[uuid.UUID]int      // in-flight jobs per user
	active  int
	stopped bool
}

func New(cfg Config) *Pool {
	if cfg.Workers < 1 {
		cfg.Workers = 1
	}
	if cfg.PerUser < 1 {
		cfg.PerUser = 1
	}
	p := &Pool{
		cfg:     cfg,
		pending: map[uuid.UUID]struct{}{},
		running: map[uuid.UUID]int{},
	}
	p.cond = sync.NewCond(&p.mu)
	return p
}

// Start brings the dispatcher up. Jobs submitted before it runs simply wait.
func (p *Pool) Start() { go p.dispatch() }

// Stop ends dispatching. Work already in flight is deliberately left alone:
// it holds its own context and is in the middle of committing a file. What
// does not survive the process is recovered by the boot sweep.
func (p *Pool) Stop() {
	p.mu.Lock()
	p.stopped = true
	p.mu.Unlock()
	p.cond.Broadcast()
}

// Submit enqueues a job. A session already queued or running is ignored —
// assembling one session twice at once would have two workers writing two
// staging files and both committing them.
func (p *Pool) Submit(j Job) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return false
	}
	if _, dup := p.pending[j.SessionID]; dup {
		return false
	}
	p.pending[j.SessionID] = struct{}{}
	p.queue = append(p.queue, j)
	p.cond.Signal()
	return true
}

func (p *Pool) dispatch() {
	for {
		p.mu.Lock()
		for !p.stopped && p.nextEligible() < 0 {
			p.cond.Wait()
		}
		if p.stopped {
			p.mu.Unlock()
			return
		}
		i := p.nextEligible()
		job := p.queue[i]
		p.queue = append(p.queue[:i], p.queue[i+1:]...)
		p.active++
		p.running[job.UserID]++
		p.mu.Unlock()

		go p.work(job)
	}
}

// nextEligible is the index of the first queued job that may start now, or -1.
// Skipping over a job whose user is already busy keeps one user's queue of
// large files from starving everyone else's.
func (p *Pool) nextEligible() int {
	if p.active >= p.cfg.Workers {
		return -1
	}
	for i, j := range p.queue {
		if p.running[j.UserID] < p.cfg.PerUser {
			return i
		}
	}
	return -1
}

func (p *Pool) work(job Job) {
	again := p.cfg.Run(job)

	p.mu.Lock()
	p.active--
	p.running[job.UserID]--
	if p.running[job.UserID] == 0 {
		delete(p.running, job.UserID)
	}
	delete(p.pending, job.SessionID)
	p.mu.Unlock()
	p.cond.Signal()

	if !again {
		return
	}
	// The delay is the pool's business, not the runner's: a volume that just
	// failed a read will fail the next one too, and a hot retry loop turns one
	// broken upload into a busy worker.
	if p.cfg.RetryDelay > 0 {
		time.Sleep(p.cfg.RetryDelay)
	}
	p.Submit(job)
}
