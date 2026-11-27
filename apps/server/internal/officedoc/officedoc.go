// Package officedoc builds and signs the editor configuration handed to an
// externally operated, OnlyOffice-API-compatible document server ("Euro-
// Office") for preview and, where the requester has write access, editing
// of Office documents (ADR-0027, edit/save-back per ADR-0033).
//
// This is deliberately a separate signing scheme from apps/server/internal/auth:
// the config-JWT is verified by the document server itself, using a secret
// shared only between this server and the document server — never the app's
// session JWTSecret. The callback token (CallbackClaims) reuses the same
// secret but is never sent to nor verified by the document server itself —
// it is opaque to it, only ever parsed by this server when the save-callback
// arrives (ADR-0033).
package officedoc

import (
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Config is the resolved document-server wiring. Both fields empty means
// the feature is off; a half-set config is rejected at startup by
// config.validate, so handlers only ever see "both set" or "both empty".
type Config struct {
	URL       string
	JWTSecret string
}

// Enabled reports whether the document server is configured.
func (c Config) Enabled() bool {
	return c.URL != "" && c.JWTSecret != ""
}

// configTokenTTL bounds how long an issued editor config remains valid.
// Short-lived by design (ADR-0027): re-opening the preview mints a fresh one.
const configTokenTTL = 5 * time.Minute

// callbackTokenTTL bounds how long a save-back callback token stays valid.
// Deliberately far longer than configTokenTTL: unlike the read-only fetch
// token (re-minted on every open), one callback token backs an entire
// editing session — the document server keeps calling it back until the
// user closes the document, which routinely outlasts a 5-minute window
// (ADR-0033). A save arriving after expiry fails loudly (document server
// reports "changes not saved") rather than being accepted against a stale
// authorization.
const callbackTokenTTL = 24 * time.Hour

// Type describes one Office format to the document server: the OnlyOffice
// fileType/documentType pair, plus whether the format survives a round trip.
type Type struct {
	FileType     string
	DocumentType string
	// Editable is true only for formats the document server edits natively
	// and saves back in the *same* format — OOXML without macros, and ODF.
	// Everything else it converts to OOXML on open, so a save-back would
	// store converted bytes under the original extension (a .doc holding
	// DOCX, macros silently dropped from a .docm). Those formats stay
	// view-only regardless of the requester's write access.
	Editable bool
}

// format is the stored half of a Type: FileType is always the extension
// itself, so keeping it out of the table removes the chance of the two
// drifting apart.
type format struct {
	documentType string
	editable     bool
}

// extTypes maps every supported extension (lower-case, no dot) to its format.
// Scope is the document server's Office formats (ADR-0027, extended per
// ADR-0049): word processing, spreadsheets and presentations. Deliberately
// excluded are its PDF-family types (pdf/djvu/xps/oxps — the browser's own
// PDF preview handles those), the image types it lists for insertion, and
// the plain-text/markup formats this app already opens in its own text
// editor (txt, csv, tsv, md, xml, html) or dedicated viewers (epub).
var extTypes = map[string]format{
	// Word processing
	"docx":  {"word", true},
	"dotx":  {"word", true},
	"odt":   {"word", true},
	"ott":   {"word", true},
	"fodt":  {"word", true},
	"doc":   {"word", false},
	"dot":   {"word", false},
	"docm":  {"word", false},
	"dotm":  {"word", false},
	"rtf":   {"word", false},
	"sxw":   {"word", false},
	"stw":   {"word", false},
	"wps":   {"word", false},
	"wpt":   {"word", false},
	"pages": {"word", false},

	// Spreadsheets
	"xlsx":    {"cell", true},
	"xltx":    {"cell", true},
	"ods":     {"cell", true},
	"ots":     {"cell", true},
	"fods":    {"cell", true},
	"xls":     {"cell", false},
	"xlt":     {"cell", false},
	"xlsb":    {"cell", false},
	"xlsm":    {"cell", false},
	"xltm":    {"cell", false},
	"sxc":     {"cell", false},
	"et":      {"cell", false},
	"ett":     {"cell", false},
	"numbers": {"cell", false},

	// Presentations
	"pptx": {"slide", true},
	"potx": {"slide", true},
	"ppsx": {"slide", true},
	"odp":  {"slide", true},
	"otp":  {"slide", true},
	"fodp": {"slide", true},
	"ppt":  {"slide", false},
	"pot":  {"slide", false},
	"pps":  {"slide", false},
	"pptm": {"slide", false},
	"potm": {"slide", false},
	"ppsm": {"slide", false},
	"odg":  {"slide", false},
	"sxi":  {"slide", false},
	"dps":  {"slide", false},
	"dpt":  {"slide", false},
	"key":  {"slide", false},
}

// TypeForPath returns the Type for a path's extension, and false when the
// extension is not a supported Office format.
func TypeForPath(path string) (Type, bool) {
	ext := strings.TrimPrefix(strings.ToLower(pathExt(path)), ".")
	f, ok := extTypes[ext]
	if !ok {
		return Type{}, false
	}
	return Type{FileType: ext, DocumentType: f.documentType, Editable: f.editable}, true
}

func pathExt(p string) string {
	i := strings.LastIndexByte(p, '.')
	if i < 0 {
		return ""
	}
	return p[i:]
}

// BuildOpts carries everything needed to build one document's editor config.
type BuildOpts struct {
	// Key uniquely identifies this document *version* to the document
	// server's own caching — must change whenever the content changes.
	Key      string
	Title    string
	FileType string
	DocType  string
	// DocumentURL is the URL the document server fetches the raw bytes
	// from (an opaque, short-lived, server-minted token URL — never a
	// URL requiring our normal Bearer auth, since the document server
	// cannot send it).
	DocumentURL string
	// UserID/Username are surfaced to OnlyOffice for collaboration UI
	// (co-editing cursors etc.).
	UserID   string
	Username string
	// Editable switches the editor into edit mode with save-back
	// (ADR-0033). false keeps the original read-only behaviour (ADR-0027).
	// The caller decides this from the requester's write access — never
	// this package.
	Editable bool
	// CallbackURL is where the document server POSTs edited bytes back.
	// Required (and only used) when Editable is true.
	CallbackURL string
}

// BuildAndSign renders the OnlyOffice editor-config JSON and returns it
// alongside a JWT (HS256, signed with cfg.JWTSecret) covering the whole
// config object, as required by the document server's own JWT validation.
// permissions.edit / editorConfig.mode follow opts.Editable (ADR-0027 for
// the view-only case, ADR-0033 for edit + save-back).
func BuildAndSign(cfg Config, opts BuildOpts) (config map[string]any, token string, err error) {
	if !cfg.Enabled() {
		return nil, "", fmt.Errorf("document server not configured")
	}

	document := map[string]any{
		"fileType": opts.FileType,
		"key":      opts.Key,
		"title":    opts.Title,
		"url":      opts.DocumentURL,
		"permissions": map[string]any{
			"edit":     opts.Editable,
			"download": false,
			"print":    true,
		},
	}
	editorConfig := map[string]any{
		"mode": "view",
		"user": map[string]any{
			"id":   opts.UserID,
			"name": opts.Username,
		},
	}
	if opts.Editable {
		editorConfig["mode"] = "edit"
		editorConfig["callbackUrl"] = opts.CallbackURL
	}
	config = map[string]any{
		"document":     document,
		"documentType": opts.DocType,
		"editorConfig": editorConfig,
	}

	claims := jwt.MapClaims{
		"document":     document,
		"documentType": opts.DocType,
		"editorConfig": editorConfig,
		"exp":          time.Now().Add(configTokenTTL).Unix(),
		"iat":          time.Now().Unix(),
	}
	jwtToken := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := jwtToken.SignedString([]byte(cfg.JWTSecret))
	if err != nil {
		return nil, "", fmt.Errorf("signing document-server config: %w", err)
	}
	config["token"] = signed
	return config, signed, nil
}

// CallbackClaims identifies the document, its owner/actor and the
// server_version it was opened at, carried inside the stateless, signed
// callback URL handed to the document server as editorConfig.callbackUrl
// (ADR-0033). Stateless by design: an editing session can run far longer
// than the 5-minute view-fetch token, so this avoids server-side session
// storage/TTL bookkeeping for saves entirely — the signature and exp claim
// are the only things HandleOfficeCallback needs to trust the request.
type CallbackClaims struct {
	OwnerID     string
	RootID      string
	Path        string
	ActorUserID string
	// GrantID anchors the recipient-side permission when the token was minted
	// for a Shared Mount. The save path re-resolves it against the live grant,
	// so revoking the share — or just its write flag — stops further saves
	// instead of leaving them authorised for the token's whole lifetime
	//. Empty for a document in the actor's own root, where owning it
	// is the permission.
	GrantID string
	// BaseVersion is the file's server_version at the moment the editor
	// config was minted — passed back to the coordinator as If-Match so a
	// clean save (nobody else wrote meanwhile) updates in place, while a
	// concurrent write in between routes through the existing
	// conflict-loser-copy path (ADR-0003) instead of silently overwriting
	// or discarding the edit.
	BaseVersion int64
}

// SignCallbackToken signs c into a compact JWT (HS256, cfg.JWTSecret).
func SignCallbackToken(secret string, c CallbackClaims) (string, error) {
	claims := jwt.MapClaims{
		"owner_id":     c.OwnerID,
		"root_id":      c.RootID,
		"path":         c.Path,
		"actor_id":     c.ActorUserID,
		"grant_id":     c.GrantID,
		"base_version": c.BaseVersion,
		"exp":          time.Now().Add(callbackTokenTTL).Unix(),
		"iat":          time.Now().Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("signing office callback token: %w", err)
	}
	return signed, nil
}

// ParseCallbackToken verifies and decodes a callback token minted by
// SignCallbackToken. Returns an error for a bad signature, an expired
// token, or one missing a required claim.
func ParseCallbackToken(secret, tokenStr string) (CallbackClaims, error) {
	var out CallbackClaims
	parsed, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil || !parsed.Valid {
		return out, fmt.Errorf("invalid office callback token: %w", err)
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return out, fmt.Errorf("invalid office callback token claims")
	}
	out.OwnerID, _ = claims["owner_id"].(string)
	out.RootID, _ = claims["root_id"].(string)
	out.Path, _ = claims["path"].(string)
	out.ActorUserID, _ = claims["actor_id"].(string)
	// Optional by design: absent for an own-root document.
	out.GrantID, _ = claims["grant_id"].(string)
	if bv, ok := claims["base_version"].(float64); ok {
		out.BaseVersion = int64(bv)
	}
	if out.OwnerID == "" || out.RootID == "" || out.Path == "" || out.ActorUserID == "" {
		return out, fmt.Errorf("incomplete office callback token claims")
	}
	return out, nil
}
