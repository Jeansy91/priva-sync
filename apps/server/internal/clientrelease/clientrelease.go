// Package clientrelease describes the desktop client build that belongs to
// this server (ADR-0088). The server only ever offers the client version equal
// to its own version, and points the updater straight at the GitHub release
// assets. Asset names and updater signatures per platform come from a
// manifest that CI writes before the server image is built and that is
// embedded into the binary. The checked-in manifest is empty, so local builds
// and tests work without CI and simply offer no update.
package clientrelease

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// RepoURL is the GitHub repository that hosts the client release assets.
// Deliberately a constant, not configuration (ADR-0088).
const RepoURL = "https://github.com/Jeansy91/priva-sync"

//go:embed manifest.json
var embeddedManifest []byte

// Platform is one updater target's entry: the release asset the updater
// downloads, the content of its Tauri updater signature (.sig) file and the
// installer a human downloads for a first install. Installer equals Asset on
// Windows and Linux; on macOS the asset is the updater archive and the
// installer the .dmg. Installer is optional so older manifests stay valid.
type Platform struct {
	Asset     string `json:"asset"`
	Signature string `json:"signature"`
	Installer string `json:"installer,omitempty"`
}

// Manifest is the build-time manifest. Version is the client/server version
// the assets were built for; Platforms is keyed by Tauri updater target
// (e.g. "windows-x86_64"). PubDate is optional (RFC 3339).
type Manifest struct {
	Version   string              `json:"version"`
	PubDate   string              `json:"pub_date,omitempty"`
	Platforms map[string]Platform `json:"platforms"`
}

// Update is the Tauri v2 updater response body.
type Update struct {
	Version   string `json:"version"`
	PubDate   string `json:"pub_date,omitempty"`
	URL       string `json:"url"`
	Signature string `json:"signature"`
}

// Embedded returns the manifest compiled into the binary.
func Embedded() (Manifest, error) {
	return Parse(embeddedManifest)
}

// Parse decodes and validates a manifest. Every asset name must be a plain
// file name because it is appended to the release download URL.
func Parse(data []byte) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("clientrelease: decode manifest: %w", err)
	}
	for target, p := range m.Platforms {
		if !ValidTarget(target) {
			return Manifest{}, fmt.Errorf("clientrelease: unknown target %q", target)
		}
		if !validAsset(p.Asset) || p.Signature == "" {
			return Manifest{}, fmt.Errorf("clientrelease: incomplete entry for %q", target)
		}
		if p.Installer != "" && !validAsset(p.Installer) {
			return Manifest{}, fmt.Errorf("clientrelease: invalid installer for %q", target)
		}
	}
	if len(m.Platforms) > 0 && m.Version == "" {
		return Manifest{}, fmt.Errorf("clientrelease: manifest has platforms but no version")
	}
	return m, nil
}

// ValidTarget reports whether target is an updater target the server knows.
func ValidTarget(t string) bool {
	switch t {
	case "windows-x86_64",
		"darwin-x86_64",
		"darwin-aarch64",
		"linux-x86_64",
		"linux-aarch64":
		return true
	}
	return false
}

// UpdateFor returns the update to offer a client on target that currently runs
// currentVersion, or nil when there is none. An update is offered only when
// the manifest was built for serverVersion, has an entry for target, and
// currentVersion is strictly older than serverVersion. Equal and newer clients
// get nil, so a client never gets downgraded.
func (m Manifest) UpdateFor(target, currentVersion, serverVersion string) *Update {
	if m.Version == "" || m.Version != serverVersion {
		return nil
	}
	p, ok := m.Platforms[target]
	if !ok {
		return nil
	}
	if CompareVersion(serverVersion, currentVersion) <= 0 {
		return nil
	}
	return &Update{
		Version:   serverVersion,
		PubDate:   m.PubDate,
		URL:       RepoURL + "/releases/download/v" + strings.TrimPrefix(serverVersion, "v") + "/" + p.Asset,
		Signature: p.Signature,
	}
}

// Download is one installer offered on the download page.
type Download struct {
	Target string `json:"target"`
	File   string `json:"file"`
	URL    string `json:"url"`
}

// Downloads is the response of the public download listing.
type Downloads struct {
	Version    string     `json:"version"`
	ReleaseURL string     `json:"release_url"`
	Downloads  []Download `json:"downloads"`
}

// DownloadsFor lists the installers of every platform in the manifest that
// has one, sorted by target. It is empty (and version/release_url blank) when
// the manifest was not built for serverVersion, e.g. in a local dev build.
func (m Manifest) DownloadsFor(serverVersion string) Downloads {
	out := Downloads{Downloads: []Download{}}
	if m.Version == "" || m.Version != serverVersion {
		return out
	}
	tag := "v" + strings.TrimPrefix(serverVersion, "v")
	targets := make([]string, 0, len(m.Platforms))
	for t, p := range m.Platforms {
		if p.Installer != "" {
			targets = append(targets, t)
		}
	}
	sort.Strings(targets)
	if len(targets) == 0 {
		return out
	}
	out.Version = strings.TrimPrefix(serverVersion, "v")
	out.ReleaseURL = RepoURL + "/releases/tag/" + tag
	for _, t := range targets {
		f := m.Platforms[t].Installer
		out.Downloads = append(out.Downloads, Download{
			Target: t, File: f, URL: RepoURL + "/releases/download/" + tag + "/" + f,
		})
	}
	return out
}

func validAsset(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	return !strings.ContainsAny(s, `/\?#%`)
}

// CompareVersion returns 1 if a > b, -1 if a < b, 0 otherwise. Treats
// major.minor.patch numerically; ignores pre-release/build suffixes.
// Unparsable input counts as 0.0.0.
func CompareVersion(a, b string) int {
	as := parseVersion(a)
	bs := parseVersion(b)
	for i := 0; i < 3; i++ {
		if as[i] != bs[i] {
			if as[i] > bs[i] {
				return 1
			}
			return -1
		}
	}
	return 0
}

func parseVersion(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	if idx := strings.IndexAny(v, "-+"); idx >= 0 {
		v = v[:idx]
	}
	parts := strings.SplitN(v, ".", 3)
	var out [3]int
	for i := 0; i < 3 && i < len(parts); i++ {
		if parts[i] == "" {
			return [3]int{}
		}
		n := 0
		for _, c := range parts[i] {
			if c < '0' || c > '9' {
				return [3]int{}
			}
			n = n*10 + int(c-'0')
		}
		out[i] = n
	}
	return out
}
