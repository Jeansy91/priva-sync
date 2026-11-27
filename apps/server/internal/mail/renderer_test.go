package mail

import (
	"strings"
	"testing"
)

func TestRendererRendersAllTemplates(t *testing.T) {
	r, err := newRenderer()
	if err != nil {
		t.Fatalf("newRenderer: %v", err)
	}

	common := Common{BaseURL: "https://priva.test", Username: "alice", OptIn: true}

	cases := []struct {
		name string
		data any
		want string // substring that must appear in the rendered HTML
	}{
		{TplWelcome,       WelcomeData{Common: common, Email: "a@b.de", InviteURL: "https://priva.test/password-reset?token=x", ExpiresIn: "72 Stunden"}, "Willkommen"},
		{TplPasswordReset, PasswordResetData{Common: common, ResetURL: "https://priva.test/reset?t=x", ExpiresIn: "1 Stunde"},     "Passwort zurücksetzen"},
		{TplShareReceived, ShareReceivedData{Common: common, SharerName: "bob", FileName: "a.pdf", ShareURL: "https://priva.test/s/x"}, "Share"},
		{TplShareRevoked,  ShareRevokedData{Common: common, SharerName: "bob", FileName: "a.pdf"},                                    "widerrufen"},
		{TplSyncConflict,  SyncConflictData{Common: common, FilePath: "/docs/a.md", ConflictsURL: "https://priva.test/conflicts"}, "Konflikt"},
		{TplTOTPDisabled,  TOTPDisabledData{Common: common, When: "2026-04-19 10:00", IPAddress: "1.2.3.4"},                       "deaktiviert"},
		{TplQuotaWarning,  QuotaWarningData{Common: common, UsedBytes: 8_000_000_000, QuotaBytes: 10_000_000_000, Percentage: 80}, "80%"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := r.Render(LangDE, c.name, c.data)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if out.Subject == "" {
				t.Error("empty subject")
			}
			if !strings.Contains(out.HTML, c.want) {
				t.Errorf("html missing %q; got:\n%s", c.want, out.HTML)
			}
			if out.Text == "" {
				t.Error("empty text")
			}
		})
	}
}

// A multi-conflict digest must render the count plus every affected path (not
// just the first), in both languages, so a folded burst of conflicts surfaces
// all files in the single mail.
func TestRendererSyncConflictDigest(t *testing.T) {
	r, err := newRenderer()
	if err != nil {
		t.Fatalf("newRenderer: %v", err)
	}

	data := SyncConflictData{
		Common:       Common{BaseURL: "https://priva.test", Username: "alice", OptIn: true},
		FilePath:     "/docs/a.md",
		Count:        3,
		FilePaths:    []string{"/docs/a.md", "/docs/b.md", "/notes/c.md"},
		ConflictsURL: "https://priva.test/conflicts",
	}

	for _, lang := range []string{LangDE, LangEN} {
		out, err := r.Render(lang, TplSyncConflict, data)
		if err != nil {
			t.Fatalf("Render %s: %v", lang, err)
		}
		if !strings.Contains(out.Subject, "3") {
			t.Errorf("%s subject missing count; got %q", lang, out.Subject)
		}
		for _, p := range data.FilePaths {
			if !strings.Contains(out.HTML, p) {
				t.Errorf("%s html missing path %q; got:\n%s", lang, p, out.HTML)
			}
			if !strings.Contains(out.Text, p) {
				t.Errorf("%s text missing path %q; got:\n%s", lang, p, out.Text)
			}
		}
	}
}

func TestRendererEnglishWelcome(t *testing.T) {
	r, err := newRenderer()
	if err != nil {
		t.Fatalf("newRenderer: %v", err)
	}
	out, err := r.Render("en", TplWelcome, WelcomeData{
		Common:    Common{BaseURL: "https://priva.test", Username: "alice"},
		Email:     "a@b.de",
		InviteURL: "https://priva.test/password-reset?token=x",
		ExpiresIn: "72 hours",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out.Subject, "Welcome") {
		t.Errorf("EN subject missing 'Welcome'; got %q", out.Subject)
	}
	if !strings.Contains(out.HTML, "Welcome") {
		t.Errorf("EN html missing 'Welcome'; got:\n%s", out.HTML)
	}
	if !strings.Contains(out.HTML, "https://priva.test/password-reset?token=x") {
		t.Errorf("EN html missing invite link; got:\n%s", out.HTML)
	}
	if !strings.Contains(out.Text, "https://priva.test/password-reset?token=x") {
		t.Errorf("EN text missing invite link; got:\n%s", out.Text)
	}
}

func TestRendererUnknownLangFallsBackToEnglish(t *testing.T) {
	r, err := newRenderer()
	if err != nil {
		t.Fatalf("newRenderer: %v", err)
	}
	out, err := r.Render("fr", TplWelcome, WelcomeData{
		Common:    Common{BaseURL: "https://priva.test", Username: "alice"},
		Email:     "a@b.de",
		InviteURL: "https://priva.test/password-reset?token=x",
		ExpiresIn: "72 hours",
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(out.Subject, "Welcome") {
		t.Errorf("fallback subject not English; got %q", out.Subject)
	}
	if strings.Contains(out.HTML, "Willkommen") {
		t.Errorf("fallback rendered German content; html:\n%s", out.HTML)
	}
}

func TestRendererSetsHTMLLangAttr(t *testing.T) {
	r, err := newRenderer()
	if err != nil {
		t.Fatalf("newRenderer: %v", err)
	}
	common := Common{BaseURL: "https://priva.test", Username: "alice"}
	cases := map[string]string{
		LangDE: `<html lang="de">`,
		LangEN: `<html lang="en">`,
	}
	for lang, want := range cases {
		t.Run(lang, func(t *testing.T) {
			out, err := r.Render(lang, TplWelcome, WelcomeData{Common: common})
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if !strings.Contains(out.HTML, want) {
				t.Errorf("html missing %q; got:\n%s", want, out.HTML)
			}
		})
	}
}

func TestFormatBytes(t *testing.T) {
	cases := map[int64]string{
		0:                "0 B",
		512:              "512 B",
		1024:             "1.0 KiB",
		1_500_000:        "1.4 MiB",
		10_737_418_240:   "10.0 GiB",
	}
	for in, want := range cases {
		if got := formatBytes(in); got != want {
			t.Errorf("formatBytes(%d) = %q, want %q", in, got, want)
		}
	}
}
