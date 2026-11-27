package mail

import (
	"bytes"
	"embed"
	"fmt"
	htmltemplate "html/template"
	"reflect"
	"strings"
	texttemplate "text/template"
)

// funcMap is a single source of truth for template helpers. Both html/text
// template packages consume it via .Funcs() — the types are compatible
// because htmltemplate.FuncMap and texttemplate.FuncMap share a definition.
var funcMap = map[string]any{
	"formatBytes": formatBytes,
}

// formatBytes renders a byte count in human-friendly units (KiB/MiB/GiB).
// Integer math only — "12.34 MB" displays as "12 MB"; fine for quota mails.
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	return fmt.Sprintf("%.1f %s", float64(n)/float64(div), units[exp])
}

//go:embed templates/layout.html
//go:embed templates/de/*.html templates/de/*.txt templates/de/*.subject
//go:embed templates/en/*.html templates/en/*.txt templates/en/*.subject
var templateFS embed.FS

// Trigger names — used as keys into the renderer's template maps. Adding a
// new trigger means: dropping three files under templates/<lang>/ for each
// supported language and listing the name here.
const (
	TplWelcome       = "welcome"
	TplPasswordReset = "password_reset"
	TplShareReceived = "share_received"
	TplShareRevoked  = "share_revoked"
	TplSyncConflict  = "sync_conflict"
	TplTOTPDisabled  = "totp_disabled"
	TplQuotaWarning  = "quota_warning"
	TplEmailChangeRequest = "email_change_request"
	TplEmailChanged       = "email_changed"
)

// Supported languages. fallbackLang kicks in when Render is called with an
// empty/unknown lang code — see Render().
const (
	LangDE       = "de"
	LangEN       = "en"
	fallbackLang = LangEN
)

var (
	allTemplates = []string{
		TplWelcome,
		TplPasswordReset,
		TplShareReceived,
		TplShareRevoked,
		TplSyncConflict,
		TplTOTPDisabled,
		TplQuotaWarning,
		TplEmailChangeRequest,
		TplEmailChanged,
	}
	allLangs = []string{LangDE, LangEN}
)

// ─── Data structs ────────────────────────────────────────────────────────────

// Common is embedded in every data struct. Fields are promoted so templates
// reference them as {{.BaseURL}} etc. Lang is set by the renderer right
// before execution; callers don't need to fill it.
type Common struct {
	BaseURL  string // e.g. "https://priva.example.com" — for logo + links
	Username string // recipient display name (user.username)
	// OptIn flips the footer-unsubscribe hint — true for mails the user can
	// opt out of via user.email_notify (conflict, share, quota).
	OptIn bool
	// Lang is the BCP-47 short code ("de"/"en") used by the layout's
	// <html lang="…"> attribute. Renderer fills this — callers leave it zero.
	Lang string
}

type WelcomeData struct {
	Common
	Email     string
	InviteURL string
	ExpiresIn string // humaner Text, z.B. "72 Stunden"
}

type PasswordResetData struct {
	Common
	ResetURL  string
	ExpiresIn string // humaner Text, z.B. "1 Stunde"
}

type ShareReceivedData struct {
	Common
	SharerName string
	FileName   string
	ShareURL   string
}

// ShareRevokedData drives the mail that informs the target user when
// the giver explicitly revoked a user-share. See ADR-0010.
type ShareRevokedData struct {
	Common
	SharerName string
	FileName   string
}

// SyncConflictData drives the conflict mail. A single sync that conflicts many
// files is folded into one digest: Count carries the total and FilePaths lists
// them, while FilePath stays the first/only path so the Count<=1 wording keeps
// working unchanged.
type SyncConflictData struct {
	Common
	FilePath     string
	Count        int
	FilePaths    []string
	ConflictsURL string
}

type TOTPDisabledData struct {
	Common
	When      string // formatiertes Datum
	IPAddress string
}

type EmailChangeRequestData struct {
	Common
	NewEmail  string
	ConfirmURL string
	ExpiresIn string
}

type EmailChangedData struct {
	Common
	OldEmail string
	NewEmail string
}

type QuotaWarningData struct {
	Common
	UsedBytes  int64
	QuotaBytes int64
	Percentage int // 80 oder 95
}

// ─── Renderer ────────────────────────────────────────────────────────────────

type Rendered struct {
	Subject string
	HTML    string
	Text    string
}

type langSet struct {
	html     map[string]*htmltemplate.Template
	text     map[string]*texttemplate.Template
	subjects map[string]*texttemplate.Template
}

type renderer struct {
	bylang map[string]*langSet
}

// newRenderer parses every template at startup, for every supported lang.
// Any parse error surfaces now rather than at first send — acts as the
// DoD smoke-test.
func newRenderer() (*renderer, error) {
	r := &renderer{bylang: make(map[string]*langSet, len(allLangs))}
	for _, lang := range allLangs {
		set, err := parseLang(lang)
		if err != nil {
			return nil, err
		}
		r.bylang[lang] = set
	}
	return r, nil
}

func parseLang(lang string) (*langSet, error) {
	set := &langSet{
		html:     make(map[string]*htmltemplate.Template, len(allTemplates)),
		text:     make(map[string]*texttemplate.Template, len(allTemplates)),
		subjects: make(map[string]*texttemplate.Template, len(allTemplates)),
	}
	for _, name := range allTemplates {
		html, err := htmltemplate.New(name).Funcs(funcMap).ParseFS(templateFS,
			"templates/layout.html",
			"templates/"+lang+"/_footer.html",
			"templates/"+lang+"/"+name+".html",
		)
		if err != nil {
			return nil, fmt.Errorf("mail: parsing %s/%s.html: %w", lang, name, err)
		}
		set.html[name] = html

		text, err := texttemplate.New(name+".txt").Funcs(funcMap).ParseFS(templateFS, "templates/"+lang+"/"+name+".txt")
		if err != nil {
			return nil, fmt.Errorf("mail: parsing %s/%s.txt: %w", lang, name, err)
		}
		set.text[name] = text

		subj, err := texttemplate.New(name+".subject").Funcs(funcMap).ParseFS(templateFS, "templates/"+lang+"/"+name+".subject")
		if err != nil {
			return nil, fmt.Errorf("mail: parsing %s/%s.subject: %w", lang, name, err)
		}
		set.subjects[name] = subj
	}
	return set, nil
}

// resolveLang maps a requested code to a parsed language set. Empty or
// unknown codes fall through to fallbackLang. This is the server-side
// counterpart of packages/i18n/resolveLanguage.
func (r *renderer) resolveLang(lang string) (string, *langSet) {
	if set, ok := r.bylang[lang]; ok {
		return lang, set
	}
	return fallbackLang, r.bylang[fallbackLang]
}

// Render executes subject / html / text templates for the given trigger in
// the given language. Unknown lang falls back to English. The data param
// must be the struct matching the trigger (e.g. WelcomeData for TplWelcome);
// type isn't enforced at compile time and mismatches surface as
// template-execution errors. Common.Lang is filled in automatically.
func (r *renderer) Render(lang, name string, data any) (*Rendered, error) {
	resolved, set := r.resolveLang(lang)
	if set == nil {
		return nil, fmt.Errorf("mail: no templates loaded")
	}
	subj, ok := set.subjects[name]
	if !ok {
		return nil, fmt.Errorf("mail: unknown template %q", name)
	}

	data = withLang(data, resolved)

	var buf bytes.Buffer
	if err := subj.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("mail: subject: %w", err)
	}
	subject := strings.TrimSpace(buf.String())
	buf.Reset()

	if err := set.html[name].ExecuteTemplate(&buf, "layout", data); err != nil {
		return nil, fmt.Errorf("mail: html: %w", err)
	}
	htmlBody := buf.String()
	buf.Reset()

	if err := set.text[name].Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("mail: text: %w", err)
	}
	return &Rendered{
		Subject: subject,
		HTML:    htmlBody,
		Text:    buf.String(),
	}, nil
}

// withLang returns a copy of data with the embedded Common.Lang field set
// to lang. Templates read `.Lang` from the promoted Common field, so this
// keeps callers from having to plumb the language into every data struct.
// Returns the original value if data isn't a struct embedding Common.
func withLang(data any, lang string) any {
	v := reflect.ValueOf(data)
	if !v.IsValid() {
		return data
	}
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return data
	}
	cp := reflect.New(v.Type()).Elem()
	cp.Set(v)
	common := cp.FieldByName("Common")
	if !common.IsValid() || !common.CanSet() {
		return data
	}
	field := common.FieldByName("Lang")
	if !field.IsValid() || !field.CanSet() || field.Kind() != reflect.String {
		return data
	}
	field.SetString(lang)
	return cp.Interface()
}
