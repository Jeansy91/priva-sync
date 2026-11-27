package auth

import (
	"fmt"
	"net/url"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"privasync/server/internal/model"
)

// NewWebAuthn builds the relying-party config from the server's public base
// URL. RPID is the bare hostname (no scheme/port); RPOrigins is the full
// origin the browser sends — both must track cfg.Server.BaseURL (the same
// value already used for links/redirects elsewhere), not a separately
// configured value that could drift from it.
//
// ResidentKey is forced to "required": login always goes through
// BeginDiscoverableLogin (usernameless, no allowCredentials list), which only
// surfaces client-side-resident (discoverable) credentials. Leaving this at
// the library default ("discouraged") lets some authenticators create a
// non-discoverable credential that registers fine but can never be found
// again at login.
func NewWebAuthn(baseURL, displayName string) (*webauthn.WebAuthn, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parsing base url for webauthn RPID: %w", err)
	}
	return webauthn.New(&webauthn.Config{
		RPDisplayName: displayName,
		RPID:          u.Hostname(),
		RPOrigins:     []string{u.Scheme + "://" + u.Host},
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey: protocol.ResidentKeyRequirementRequired,
		},
	})
}

// WebAuthnUser adapts a model.User plus its already-loaded credentials to
// the go-webauthn library's User interface. Construct fresh with the data
// needed for the ceremony at hand — this type has no DB access itself,
// keeping internal/auth free of repository deps like the rest of the
// package.
type WebAuthnUser struct {
	user        *model.User
	credentials []webauthn.Credential
}

func NewWebAuthnUser(user *model.User, credentials []webauthn.Credential) *WebAuthnUser {
	return &WebAuthnUser{user: user, credentials: credentials}
}

func (u *WebAuthnUser) WebAuthnID() []byte                        { return u.user.ID[:] }
func (u *WebAuthnUser) WebAuthnName() string                      { return u.user.Username }
func (u *WebAuthnUser) WebAuthnDisplayName() string                { return u.user.Username }
func (u *WebAuthnUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }
