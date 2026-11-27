package officedoc

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestConfigEnabled(t *testing.T) {
	if (Config{}).Enabled() {
		t.Fatal("empty config must be disabled")
	}
	if (Config{URL: "https://x"}).Enabled() {
		t.Fatal("half-set config must be disabled")
	}
	if !(Config{URL: "https://x", JWTSecret: "s"}).Enabled() {
		t.Fatal("fully-set config must be enabled")
	}
}

func TestTypeForPath(t *testing.T) {
	cases := map[string]struct {
		docType  string
		editable bool
		ok       bool
	}{
		"a/b/report.docx": {"word", true, true},
		"sheet.XLSX":      {"cell", true, true},
		"deck.pptx":       {"slide", true, true},
		// ODF round-trips natively, so it stays editable (ADR-0049).
		"text.odt":   {"word", true, true},
		"calc.ods":   {"cell", true, true},
		"slides.odp": {"slide", true, true},
		// Legacy and macro-enabled formats open, but only for viewing:
		// the document server converts them to OOXML on open.
		"legacy.doc":  {"word", false, true},
		"legacy.xls":  {"cell", false, true},
		"legacy.ppt":  {"slide", false, true},
		"macros.docm": {"word", false, true},
		"macros.xlsm": {"cell", false, true},
		// Handled elsewhere in this app, never routed to the document server.
		"notes.txt": {"", false, false},
		"scan.pdf":  {"", false, false},
		"book.epub": {"", false, false},
		"data.csv":  {"", false, false},
		"noext":     {"", false, false},
	}
	for path, want := range cases {
		got, ok := TypeForPath(path)
		if ok != want.ok {
			t.Errorf("TypeForPath(%q) ok=%v, want %v", path, ok, want.ok)
			continue
		}
		if !ok {
			continue
		}
		if got.DocumentType != want.docType || got.Editable != want.editable {
			t.Errorf("TypeForPath(%q) = %+v, want documentType=%q editable=%v",
				path, got, want.docType, want.editable)
		}
	}
}

func TestBuildAndSignDisabled(t *testing.T) {
	if _, _, err := BuildAndSign(Config{}, BuildOpts{}); err == nil {
		t.Fatal("expected error when document server disabled")
	}
}

// TestCallbackTokenRoundTrip covers the happy path: claims signed by
// SignCallbackToken come back unchanged through ParseCallbackToken.
func TestCallbackTokenRoundTrip(t *testing.T) {
	want := CallbackClaims{
		OwnerID: "owner-1", RootID: "root-1", Path: "/report.docx",
		ActorUserID: "actor-1", BaseVersion: 7,
	}
	token, err := SignCallbackToken("shared-secret", want)
	if err != nil {
		t.Fatalf("SignCallbackToken: %v", err)
	}
	got, err := ParseCallbackToken("shared-secret", token)
	if err != nil {
		t.Fatalf("ParseCallbackToken: %v", err)
	}
	if got != want {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", got, want)
	}
}

// TestParseCallbackToken_WrongSecretRejected — a token must not verify
// against any secret other than the one it was signed with.
func TestParseCallbackToken_WrongSecretRejected(t *testing.T) {
	token, err := SignCallbackToken("right-secret", CallbackClaims{
		OwnerID: "o", RootID: "r", Path: "/p", ActorUserID: "a",
	})
	if err != nil {
		t.Fatalf("SignCallbackToken: %v", err)
	}
	if _, err := ParseCallbackToken("wrong-secret", token); err == nil {
		t.Fatal("expected error for wrong secret")
	}
}

// TestParseCallbackToken_ExpiredRejected — a save-callback token past its
// exp claim must fail even with the correct secret (ADR-0033: a save
// arriving after expiry must fail loudly, not be accepted).
func TestParseCallbackToken_ExpiredRejected(t *testing.T) {
	claims := jwt.MapClaims{
		"owner_id": "o", "root_id": "r", "path": "/p", "actor_id": "a",
		"base_version": float64(1),
		"exp":          time.Now().Add(-time.Hour).Unix(),
		"iat":          time.Now().Add(-2 * time.Hour).Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte("shared-secret"))
	if err != nil {
		t.Fatalf("sign expired token: %v", err)
	}
	if _, err := ParseCallbackToken("shared-secret", signed); err == nil {
		t.Fatal("expected error for expired token")
	}
}

// TestParseCallbackToken_IncompleteClaimsRejected — a structurally valid,
// correctly-signed token missing a required claim must still be rejected
// rather than handed to the caller with zero-value fields.
func TestParseCallbackToken_IncompleteClaimsRejected(t *testing.T) {
	claims := jwt.MapClaims{
		"owner_id": "o", "root_id": "r",
		// path and actor_id deliberately omitted
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte("shared-secret"))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	if _, err := ParseCallbackToken("shared-secret", signed); err == nil {
		t.Fatal("expected error for incomplete claims")
	}
}

func TestBuildAndSignViewOnly(t *testing.T) {
	cfg := Config{URL: "https://docserver.example", JWTSecret: "shared-secret"}
	config, token, err := BuildAndSign(cfg, BuildOpts{
		Key: "file-1-v2", Title: "report.docx", FileType: "docx", DocType: "word",
		DocumentURL: "https://app.example/office/documents/tok123",
		UserID:      "u1", Username: "Alice",
	})
	if err != nil {
		t.Fatalf("BuildAndSign: %v", err)
	}
	if token == "" || config["token"] != token {
		t.Fatal("expected config.token to equal returned token")
	}
	doc, ok := config["document"].(map[string]any)
	if !ok {
		t.Fatal("expected document object in config")
	}
	perms, ok := doc["permissions"].(map[string]any)
	if !ok || perms["edit"] != false {
		t.Fatalf("expected permissions.edit == false, got %v", perms)
	}

	parsed, err := jwt.Parse(token, func(*jwt.Token) (interface{}, error) {
		return []byte(cfg.JWTSecret), nil
	})
	if err != nil || !parsed.Valid {
		t.Fatalf("token does not verify against document-server secret: %v", err)
	}
	if _, err := jwt.Parse(token, func(*jwt.Token) (interface{}, error) {
		return []byte("wrong-secret"), nil
	}); err == nil {
		t.Fatal("token must not verify against a different secret")
	}
}
