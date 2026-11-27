// Package mail is a thin SMTP wrapper with async send queue.
//
// The queue is an in-process buffered channel drained by a single worker
// goroutine. On failure the message is logged and dropped — no persistence,
// no retries. This is fine for transactional mails where the next trigger
// (next upload, next share) will produce fresh messages and the SMTP server
// outage is operator-visible.
package mail

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	gomail "github.com/wneessen/go-mail"

	"privasync/server/internal/config"
)

const queueSize = 128

// Message is a single outbound mail. BodyHTML may be empty; BodyText is
// used as fallback and as plain-text part.
type Message struct {
	To       string
	Subject  string
	BodyHTML string
	BodyText string
}

// Mailer owns the SMTP client and the background worker. Send() is
// non-blocking (enqueue); the worker does the actual SMTP conversation.
type Mailer struct {
	cfg      config.SMTPConfig
	client   *gomail.Client
	renderer *renderer
	queue    chan *Message
	wg       sync.WaitGroup
	stop     chan struct{}
	enabled  bool
}

// New constructs a Mailer. When cfg.Enabled=false the mailer is a no-op:
// Send() discards messages silently with a debug log. This keeps call-sites
// free of nil-checks.
func New(cfg config.SMTPConfig) (*Mailer, error) {
	// Parse templates always — smoke test. Template errors
	// should surface on server start, not on first send.
	rnd, err := newRenderer()
	if err != nil {
		return nil, err
	}

	m := &Mailer{
		cfg:      cfg,
		renderer: rnd,
		queue:    make(chan *Message, queueSize),
		stop:     make(chan struct{}),
	}
	if !cfg.Enabled {
		slog.Info("mail: disabled via config — all sends are no-op")
		return m, nil
	}

	opts := []gomail.Option{
		gomail.WithPort(cfg.Port),
	}
	switch cfg.TLSMode {
	case "none":
		opts = append(opts, gomail.WithTLSPolicy(gomail.NoTLS))
	case "starttls":
		opts = append(opts, gomail.WithTLSPolicy(gomail.TLSMandatory))
	case "tls":
		opts = append(opts, gomail.WithSSLPort(false), gomail.WithTLSPolicy(gomail.NoTLS))
	default:
		return nil, fmt.Errorf("mail: invalid tls_mode %q", cfg.TLSMode)
	}
	if cfg.Username != "" {
		opts = append(opts,
			gomail.WithSMTPAuth(gomail.SMTPAuthAutoDiscover),
			gomail.WithUsername(cfg.Username),
			gomail.WithPassword(cfg.Password),
		)
	}

	client, err := gomail.NewClient(cfg.Host, opts...)
	if err != nil {
		return nil, fmt.Errorf("mail: building client: %w", err)
	}
	m.client = client
	m.enabled = true

	// go-mail handles implicit-TLS via NewClient with WithSSLPort(true) OR
	// by letting the library detect port 465. We set it explicitly here
	// since we carry the mode in config.
	if cfg.TLSMode == "tls" {
		client.SetSSL(true)
	}

	return m, nil
}

// Start spawns the worker goroutine. Call Stop at shutdown to drain.
func (m *Mailer) Start(ctx context.Context) {
	if !m.enabled {
		return
	}
	m.wg.Add(1)
	//#nosec G118 -- the mailer is a process-lifetime background worker; `ctx` is the app context, there is no request-scoped context to inherit here
	go m.run(ctx)
}

// Stop signals the worker to drain remaining messages, then waits.
func (m *Mailer) Stop() {
	if !m.enabled {
		return
	}
	close(m.stop)
	m.wg.Wait()
}

// Send enqueues a message. Returns ErrQueueFull if the queue is saturated
// (unlikely for transactional volume). When the mailer is disabled Send
// silently succeeds.
func (m *Mailer) Send(msg *Message) error {
	if !m.enabled {
		slog.Debug("mail: noop send", "to", msg.To, "subject", msg.Subject)
		return nil
	}
	select {
	case m.queue <- msg:
		return nil
	default:
		return ErrQueueFull
	}
}

// ErrQueueFull is returned when Send can't enqueue because the worker is
// behind. Callers should log and drop — never block a request on mail.
var ErrQueueFull = errors.New("mail: queue full")

// SendTemplate renders a named template in the given language and enqueues
// the result. It's the preferred API for trigger wiring. Empty/unknown
// lang falls back to English; pass user.PreferredLanguage when available.
func (m *Mailer) SendTemplate(to, lang, name string, data any) error {
	rendered, err := m.renderer.Render(lang, name, data)
	if err != nil {
		return err
	}
	return m.Send(&Message{
		To:       to,
		Subject:  rendered.Subject,
		BodyHTML: rendered.HTML,
		BodyText: rendered.Text,
	})
}

func (m *Mailer) run(ctx context.Context) {
	defer m.wg.Done()
	for {
		select {
		case <-m.stop:
			// Drain remaining queued messages with a short deadline.
			drainCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			m.drain(drainCtx)
			cancel()
			return
		case <-ctx.Done():
			return
		case msg := <-m.queue:
			m.dispatch(ctx, msg)
		}
	}
}

func (m *Mailer) drain(ctx context.Context) {
	for {
		select {
		case msg := <-m.queue:
			m.dispatch(ctx, msg)
		default:
			return
		}
	}
}

func (m *Mailer) dispatch(ctx context.Context, msg *Message) {
	gm := gomail.NewMsg()
	if err := gm.FromFormat(m.cfg.FromName, m.cfg.FromEmail); err != nil {
		slog.Warn("mail: invalid from address", "error", err)
		return
	}
	if err := gm.To(msg.To); err != nil {
		slog.Warn("mail: invalid to address", "to", msg.To, "error", err)
		return
	}
	gm.Subject(msg.Subject)
	if msg.BodyText != "" {
		gm.SetBodyString(gomail.TypeTextPlain, msg.BodyText)
	}
	if msg.BodyHTML != "" {
		if msg.BodyText != "" {
			gm.AddAlternativeString(gomail.TypeTextHTML, msg.BodyHTML)
		} else {
			gm.SetBodyString(gomail.TypeTextHTML, msg.BodyHTML)
		}
	}

	if err := m.client.DialAndSendWithContext(ctx, gm); err != nil {
		slog.Warn("mail: send failed", "to", msg.To, "subject", msg.Subject, "error", err)
		return
	}
	slog.Info("mail: sent", "to", msg.To, "subject", msg.Subject)
}

// VerifyConnection opens and closes an SMTP connection. Useful for startup
// health-check and cmd/mailtest.
func (m *Mailer) VerifyConnection(ctx context.Context) error {
	if !m.enabled {
		return errors.New("mail: disabled")
	}
	if err := m.client.DialWithContext(ctx); err != nil {
		return fmt.Errorf("mail: dial: %w", err)
	}
	return m.client.Close()
}
