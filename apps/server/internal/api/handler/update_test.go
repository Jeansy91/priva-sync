package handler

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"privasync/server/internal/clientrelease"
)

func newTestUpdateHandler(t *testing.T) *UpdateHandler {
	t.Helper()
	m, err := clientrelease.Parse([]byte(`{"version":"2.0.0","platforms":{
		"linux-x86_64":{"asset":"privasync-linux-x86_64.AppImage","signature":"sig"}}}`))
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	return NewUpdateHandler(m, "2.0.0")
}

func serveFeed(h *UpdateHandler, target, version string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "/updates/"+target+"/"+version, nil)
	r = withURLParams(r, "target", target, "version", version)
	w := httptest.NewRecorder()
	h.ServeManifest(w, r)
	return w
}

func TestServeManifest_RejectsInvalidTarget(t *testing.T) {
	w := serveFeed(newTestUpdateHandler(t), "unknown-arch", "1.0.0")
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestServeManifest_OffersServerVersionFromGitHub(t *testing.T) {
	w := serveFeed(newTestUpdateHandler(t), "linux-x86_64", "1.0.0")
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var got map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	wantURL := clientrelease.RepoURL + "/releases/download/v2.0.0/privasync-linux-x86_64.AppImage"
	if got["version"] != "2.0.0" || got["url"] != wantURL || got["signature"] != "sig" {
		t.Errorf("response = %v, want url %s", got, wantURL)
	}
}

func TestServeManifest_NoContent(t *testing.T) {
	h := newTestUpdateHandler(t)
	for name, c := range map[string][2]string{
		"equal version":  {"linux-x86_64", "2.0.0"},
		"newer client":   {"linux-x86_64", "2.1.0"},
		"target missing": {"windows-x86_64", "1.0.0"},
	} {
		if w := serveFeed(h, c[0], c[1]); w.Code != 204 {
			t.Errorf("%s: status = %d, want 204", name, w.Code)
		}
	}
}

func TestServeManifest_EmptyManifestOffersNothing(t *testing.T) {
	h := NewUpdateHandler(clientrelease.Manifest{}, "dev")
	if w := serveFeed(h, "linux-x86_64", "0.0.0"); w.Code != 204 {
		t.Fatalf("status = %d, want 204", w.Code)
	}
}

func serveDownloads(t *testing.T, manifest, serverVersion string) map[string]any {
	t.Helper()
	m, err := clientrelease.Parse([]byte(manifest))
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	w := httptest.NewRecorder()
	NewUpdateHandler(m, serverVersion).ListDownloads(w, httptest.NewRequest("GET", "/api/v1/client/downloads", nil))
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}

func TestListDownloads_ListsInstallers(t *testing.T) {
	got := serveDownloads(t, `{"version":"2.0.0","platforms":{
		"windows-x86_64":{"asset":"privasync-windows-x86_64-setup.exe","signature":"s","installer":"privasync-windows-x86_64-setup.exe"},
		"darwin-aarch64":{"asset":"privasync-darwin-aarch64.app.tar.gz","signature":"s","installer":"privasync-darwin-aarch64.dmg"},
		"linux-x86_64":{"asset":"privasync-linux-x86_64.AppImage","signature":"s"}}}`, "2.0.0")
	if got["version"] != "2.0.0" || got["release_url"] != clientrelease.RepoURL+"/releases/tag/v2.0.0" {
		t.Errorf("header = %v", got)
	}
	list, _ := got["downloads"].([]any)
	if len(list) != 2 {
		t.Fatalf("downloads = %v, want 2 entries (platform without installer skipped)", list)
	}
	first := list[0].(map[string]any)
	wantURL := clientrelease.RepoURL + "/releases/download/v2.0.0/privasync-darwin-aarch64.dmg"
	if first["target"] != "darwin-aarch64" || first["file"] != "privasync-darwin-aarch64.dmg" || first["url"] != wantURL {
		t.Errorf("first = %v, want dmg (not the updater archive)", first)
	}
}

func TestListDownloads_EmptyForDevBuildOrVersionMismatch(t *testing.T) {
	for name, c := range map[string][2]string{
		"empty manifest":   {`{"version":"","platforms":{}}`, "2.0.0"},
		"version mismatch": {`{"version":"1.0.0","platforms":{"linux-x86_64":{"asset":"a","signature":"s","installer":"a"}}}`, "2.0.0"},
	} {
		got := serveDownloads(t, c[0], c[1])
		list, ok := got["downloads"].([]any)
		if !ok || len(list) != 0 {
			t.Errorf("%s: downloads = %v, want []", name, got["downloads"])
		}
	}
}
