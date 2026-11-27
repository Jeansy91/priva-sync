package mail_test

import (
	"bufio"
	"context"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"privasync/server/internal/config"
	"privasync/server/internal/mail"
)

// fakeSMTPServer is a minimal SMTP server accepting a single connection at a
// time and speaking just enough of the protocol (EHLO/MAIL/RCPT/DATA/QUIT)
// for go-mail's DialAndSendWithContext / DialWithContext to succeed. No TLS,
// no AUTH — tests only exercise TLSMode "none" with empty credentials.
type fakeSMTPServer struct {
	ln   net.Listener
	mu   sync.Mutex
	msgs []capturedMessage
}

type capturedMessage struct {
	from string
	to   []string
	data string
}

func startFakeSMTPServer(t *testing.T) *fakeSMTPServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &fakeSMTPServer{ln: ln}
	go s.acceptLoop()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *fakeSMTPServer) port(t *testing.T) int {
	t.Helper()
	_, portStr, err := net.SplitHostPort(s.ln.Addr().String())
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return port
}

func (s *fakeSMTPServer) acceptLoop() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn)
	}
}

func (s *fakeSMTPServer) handleConn(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	writeLine := func(line string) {
		w.WriteString(line + "\r\n")
		w.Flush()
	}

	writeLine("220 fake.local ESMTP ready")

	var cur capturedMessage
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(line)

		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			writeLine("250 fake.local greets you")
		case strings.HasPrefix(upper, "MAIL FROM"):
			cur = capturedMessage{from: line}
			writeLine("250 OK")
		case strings.HasPrefix(upper, "RCPT TO"):
			cur.to = append(cur.to, line)
			writeLine("250 OK")
		case strings.HasPrefix(upper, "DATA"):
			writeLine("354 send data, end with <CRLF>.<CRLF>")
			var body strings.Builder
			for {
				dl, derr := r.ReadString('\n')
				if derr != nil {
					return
				}
				if strings.TrimRight(dl, "\r\n") == "." {
					break
				}
				body.WriteString(dl)
			}
			cur.data = body.String()
			s.mu.Lock()
			s.msgs = append(s.msgs, cur)
			s.mu.Unlock()
			writeLine("250 OK: queued")
		case strings.HasPrefix(upper, "QUIT"):
			writeLine("221 bye")
			return
		case strings.HasPrefix(upper, "RSET"):
			writeLine("250 OK")
		case strings.HasPrefix(upper, "NOOP"):
			writeLine("250 OK")
		default:
			writeLine("500 unrecognized command")
		}
	}
}

func (s *fakeSMTPServer) messages() []capturedMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]capturedMessage, len(s.msgs))
	copy(out, s.msgs)
	return out
}

func enabledCfg(t *testing.T, srv *fakeSMTPServer) config.SMTPConfig {
	return config.SMTPConfig{
		Host:      "127.0.0.1",
		Port:      srv.port(t),
		FromName:  "PrivaSync",
		FromEmail: "noreply@example.com",
		TLSMode:   "none",
		Enabled:   true,
	}
}

func TestNew_DisabledConfig_IsNoop(t *testing.T) {
	m, err := mail.New(config.SMTPConfig{Enabled: false})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Start/Stop must be safe no-ops when disabled.
	m.Start(context.Background())
	m.Stop()

	if err := m.Send(&mail.Message{To: "a@example.com", Subject: "hi"}); err != nil {
		t.Fatalf("Send on disabled mailer should succeed silently, got %v", err)
	}
}

func TestNew_InvalidTLSMode_ReturnsError(t *testing.T) {
	_, err := mail.New(config.SMTPConfig{Enabled: true, Host: "localhost", TLSMode: "bogus"})
	if err == nil {
		t.Fatal("want error for invalid tls_mode, got nil")
	}
}

func TestNew_ValidTLSModes_Succeed(t *testing.T) {
	for _, mode := range []string{"none", "starttls", "tls"} {
		t.Run(mode, func(t *testing.T) {
			_, err := mail.New(config.SMTPConfig{Enabled: true, Host: "localhost", Port: 2525, TLSMode: mode})
			if err != nil {
				t.Fatalf("New with tls_mode=%q: %v", mode, err)
			}
		})
	}
}

func TestMailer_SendTemplate_DispatchesRenderedMailOverSMTP(t *testing.T) {
	srv := startFakeSMTPServer(t)
	m, err := mail.New(enabledCfg(t, srv))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.Start(context.Background())
	defer m.Stop()

	data := mail.QuotaWarningData{
		Common:     mail.Common{BaseURL: "https://priva.test", Username: "user"},
		UsedBytes:  9_000_000_000,
		QuotaBytes: 10_000_000_000,
		Percentage: 90,
	}
	if err := m.SendTemplate("user@example.com", mail.LangDE, mail.TplQuotaWarning, data); err != nil {
		t.Fatalf("SendTemplate: %v", err)
	}

	waitFor(t, func() bool { return len(srv.messages()) == 1 }, 3*time.Second)

	got := srv.messages()[0]
	if !strings.Contains(got.to[0], "user@example.com") {
		t.Fatalf("RCPT TO = %q, want it to contain user@example.com", got.to[0])
	}
}

func TestMailer_Send_UnknownTemplate_ReturnsError(t *testing.T) {
	srv := startFakeSMTPServer(t)
	m, err := mail.New(enabledCfg(t, srv))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := m.SendTemplate("user@example.com", mail.LangDE, "does-not-exist", nil); err == nil {
		t.Fatal("want error for unknown template, got nil")
	}
}

func TestMailer_Send_InvalidRecipient_DispatchDropsSilently(t *testing.T) {
	srv := startFakeSMTPServer(t)
	m, err := mail.New(enabledCfg(t, srv))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.Start(context.Background())
	defer m.Stop()

	if err := m.Send(&mail.Message{To: "not-an-email", Subject: "hi", BodyText: "body"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	// Sentinel sent right after: since the queue is FIFO and the worker is
	// single-threaded, once this one lands the invalid one has already been
	// processed (and dropped) without crashing or blocking the worker.
	if err := m.Send(&mail.Message{To: "sentinel@example.com", Subject: "after"}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	waitFor(t, func() bool { return len(srv.messages()) == 1 }, 3*time.Second)
	got := srv.messages()[0]
	if !strings.Contains(got.to[0], "sentinel@example.com") {
		t.Fatalf("RCPT TO = %q, want the sentinel address (invalid recipient must be dropped, not delivered)", got.to[0])
	}
}

func TestSend_QueueFull_ReturnsErrQueueFull(t *testing.T) {
	srv := startFakeSMTPServer(t)
	m, err := mail.New(enabledCfg(t, srv))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Deliberately not calling Start(): nothing drains the queue, so it
	// saturates at queueSize (128).
	for i := 0; i < 128; i++ {
		if err := m.Send(&mail.Message{To: "a@example.com", Subject: "x"}); err != nil {
			t.Fatalf("Send #%d: unexpected error %v", i, err)
		}
	}
	if err := m.Send(&mail.Message{To: "a@example.com", Subject: "overflow"}); err != mail.ErrQueueFull {
		t.Fatalf("want ErrQueueFull, got %v", err)
	}
}

func TestVerifyConnection_Success(t *testing.T) {
	srv := startFakeSMTPServer(t)
	m, err := mail.New(enabledCfg(t, srv))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := m.VerifyConnection(ctx); err != nil {
		t.Fatalf("VerifyConnection: %v", err)
	}
}

func TestVerifyConnection_DisabledMailer_ReturnsError(t *testing.T) {
	m, err := mail.New(config.SMTPConfig{Enabled: false})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := m.VerifyConnection(context.Background()); err == nil {
		t.Fatal("want error for disabled mailer, got nil")
	}
}

func TestVerifyConnection_UnreachableHost_ReturnsError(t *testing.T) {
	m, err := mail.New(config.SMTPConfig{
		Enabled: true, Host: "127.0.0.1", Port: 1, TLSMode: "none",
		FromName: "x", FromEmail: "x@example.com",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	if err := m.VerifyConnection(ctx); err == nil {
		t.Fatal("want error connecting to closed port, got nil")
	}
}

func waitFor(t *testing.T, cond func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !cond() {
		t.Fatal("condition not met before timeout")
	}
}
