package officedoc

import (
	"privasync/server/internal/httpx"
)

// AllowsDocumentURL reports whether raw may be fetched as part of a save-back
// callback. Only the configured document server's own origin qualifies.
//
// The callback token is not a secret from the user: it travels in the
// /office-config response so the document server can present it later. Any
// authenticated caller can therefore replay the callback with a url of their
// choosing, and the fetched bytes land in a file they own and can download
// again -- a full read primitive against everything the server can reach
//. Pinning the origin removes the choice entirely.
//
// Deliberately *not* combined with a private-network dial guard: a
// self-hosted document server normally lives on exactly such an address
// (a compose service, a LAN host), so blocking those would break the
// supported deployment while adding nothing -- the origin is already pinned
// to one host the operator configured.
func (c Config) AllowsDocumentURL(raw string) bool {
	if !c.Enabled() {
		return false
	}
	return httpx.SameOrigin(c.URL, raw)
}
