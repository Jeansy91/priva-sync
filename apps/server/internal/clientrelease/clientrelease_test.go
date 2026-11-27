package clientrelease

import "testing"

const sample = `{"version":"1.2.0","pub_date":"2026-01-02T03:04:05Z","platforms":{
  "windows-x86_64":{"asset":"privasync-windows-x86_64-setup.exe","signature":"sigwin"},
  "darwin-aarch64":{"asset":"privasync-darwin-aarch64.app.tar.gz","signature":"sigmac"}}}`

func mustParse(t *testing.T, s string) Manifest {
	t.Helper()
	m, err := Parse([]byte(s))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return m
}

func TestEmbeddedManifestIsValidAndOffersNothing(t *testing.T) {
	m, err := Embedded()
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	if got := m.UpdateFor("windows-x86_64", "0.0.0", "dev"); got != nil {
		t.Fatalf("empty manifest must offer nothing, got %+v", got)
	}
}

func TestUpdateFor(t *testing.T) {
	m := mustParse(t, sample)

	u := m.UpdateFor("windows-x86_64", "1.1.9", "1.2.0")
	if u == nil {
		t.Fatal("want update for older client")
	}
	wantURL := RepoURL + "/releases/download/v1.2.0/privasync-windows-x86_64-setup.exe"
	if u.URL != wantURL || u.Signature != "sigwin" || u.Version != "1.2.0" || u.PubDate == "" {
		t.Fatalf("update = %+v, want url %s", u, wantURL)
	}

	cases := []struct {
		name, target, current, server string
	}{
		{"equal version", "windows-x86_64", "1.2.0", "1.2.0"},
		{"client newer (no downgrade)", "windows-x86_64", "1.3.0", "1.2.0"},
		{"target without entry", "linux-x86_64", "1.0.0", "1.2.0"},
		{"manifest built for another version", "windows-x86_64", "1.0.0", "1.3.0"},
		{"dev server", "windows-x86_64", "1.0.0", "dev"},
	}
	for _, c := range cases {
		if got := m.UpdateFor(c.target, c.current, c.server); got != nil {
			t.Errorf("%s: got %+v, want nil", c.name, got)
		}
	}
}

func TestParseRejectsBadManifests(t *testing.T) {
	bad := map[string]string{
		"not json":       `{`,
		"unknown target": `{"version":"1.0.0","platforms":{"beos-x86":{"asset":"a","signature":"s"}}}`,
		"traversal":      `{"version":"1.0.0","platforms":{"linux-x86_64":{"asset":"../a","signature":"s"}}}`,
		"empty sig":      `{"version":"1.0.0","platforms":{"linux-x86_64":{"asset":"a","signature":""}}}`,
		"no version":     `{"platforms":{"linux-x86_64":{"asset":"a","signature":"s"}}}`,
	}
	for name, s := range bad {
		if _, err := Parse([]byte(s)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestCompareVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.0", 1},
		{"1.2.0", "1.2.3", -1},
		{"2.0.0", "2.0.0", 0},
		{"v1.2.3", "1.2.3", 0},
		{"1.2.3-beta", "1.2.3", 0},
		{"1.10.0", "1.9.0", 1},
		{"dev", "0.0.0", 0},
	}
	for _, c := range cases {
		if got := CompareVersion(c.a, c.b); got != c.want {
			t.Errorf("CompareVersion(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
