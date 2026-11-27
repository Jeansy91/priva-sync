package handler

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"

	"github.com/go-webauthn/webauthn/protocol/webauthncbor"
)

// virtualAuthenticator is a minimal, self-contained FIDO2/WebAuthn authenticator
// used to drive real registration and login ceremonies through go-webauthn's
// verification code, without a browser or physical security key. It signs with
// its own ES256 key, so it only needs to agree with itself — the server-side
// handler under test is the actual thing being verified.
type virtualAuthenticator struct {
	priv   *ecdsa.PrivateKey
	credID []byte
}

func newVirtualAuthenticator(t *testing.T) *virtualAuthenticator {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate authenticator key: %v", err)
	}
	credID := make([]byte, 16)
	if _, err := rand.Read(credID); err != nil {
		t.Fatalf("generate credential id: %v", err)
	}
	return &virtualAuthenticator{priv: priv, credID: credID}
}

// coseKey CBOR-encodes the authenticator's public key in COSE_Key format
// (kty=EC2, alg=ES256, crv=P-256), matching what webauthncose.ParsePublicKey
// expects when decoding AttestedCredentialData.CredentialPublicKey.
func (v *virtualAuthenticator) coseKey(t *testing.T) []byte {
	t.Helper()
	x := make([]byte, 32)
	y := make([]byte, 32)
	v.priv.PublicKey.X.FillBytes(x)
	v.priv.PublicKey.Y.FillBytes(y)

	key := map[int64]any{
		1:  int64(2),  // kty: EC2
		3:  int64(-7), // alg: ES256
		-1: int64(1),  // crv: P-256
		-2: x,
		-3: y,
	}
	data, err := webauthncbor.Marshal(key)
	if err != nil {
		t.Fatalf("marshal COSE key: %v", err)
	}
	return data
}

// authenticatorData builds the raw (non-CBOR) authenticatorData byte layout
// per §6.1 of the spec: rpIdHash(32) + flags(1) + signCount(4) [+ attested
// credential data when withAttestedCredential is set]. signCount is always 0
// (an authenticator that doesn't support counting), which UpdateCounter
// treats as never triggering clone-detection.
func (v *virtualAuthenticator) authenticatorData(t *testing.T, rpID string, withAttestedCredential bool) []byte {
	t.Helper()
	rpIDHash := sha256.Sum256([]byte(rpID))

	var flags byte = 0x01 // UP
	flags |= 0x04         // UV
	if withAttestedCredential {
		flags |= 0x40 // AT
	}

	buf := make([]byte, 0, 128)
	buf = append(buf, rpIDHash[:]...)
	buf = append(buf, flags)
	buf = binary.BigEndian.AppendUint32(buf, 0) // signCount

	if withAttestedCredential {
		aaguid := make([]byte, 16) // zero AAGUID
		buf = append(buf, aaguid...)
		credIDLen := make([]byte, 2)
		binary.BigEndian.PutUint16(credIDLen, uint16(len(v.credID)))
		buf = append(buf, credIDLen...)
		buf = append(buf, v.credID...)
		buf = append(buf, v.coseKey(t)...)
	}

	return buf
}

func clientDataJSON(t *testing.T, ceremonyType, challengeB64, origin string) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"type":      ceremonyType,
		"challenge": challengeB64,
		"origin":    origin,
	})
	if err != nil {
		t.Fatalf("marshal clientDataJSON: %v", err)
	}
	return data
}

// registrationResponseJSON builds a full CredentialCreationResponse JSON body
// (as protocol.ParseCredentialCreationResponseBody expects) representing a
// successful navigator.credentials.create() call, using the "none"
// attestation format (no attestation statement to verify).
func (v *virtualAuthenticator) registrationResponseJSON(t *testing.T, challengeB64, rpID, origin string) []byte {
	t.Helper()
	rawAuthData := v.authenticatorData(t, rpID, true)
	cdj := clientDataJSON(t, "webauthn.create", challengeB64, origin)

	attObj := map[string]any{
		"fmt":      "none",
		"attStmt":  map[string]any{},
		"authData": rawAuthData,
	}
	attObjBytes, err := webauthncbor.Marshal(attObj)
	if err != nil {
		t.Fatalf("marshal attestationObject: %v", err)
	}

	credIDB64 := base64.RawURLEncoding.EncodeToString(v.credID)
	body := map[string]any{
		"id":    credIDB64,
		"rawId": credIDB64,
		"type":  "public-key",
		"response": map[string]any{
			"attestationObject": base64.RawURLEncoding.EncodeToString(attObjBytes),
			"clientDataJSON":    base64.RawURLEncoding.EncodeToString(cdj),
		},
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal registration response: %v", err)
	}
	return data
}

// assertionResponseJSON builds a full CredentialAssertionResponse JSON body
// representing a successful navigator.credentials.get() call for a
// discoverable (passkey) login — userHandle is always included since
// PasskeyLoginFinish only accepts discoverable-credential assertions.
func (v *virtualAuthenticator) assertionResponseJSON(t *testing.T, challengeB64, rpID, origin string, userHandle []byte) []byte {
	t.Helper()
	rawAuthData := v.authenticatorData(t, rpID, false)
	cdj := clientDataJSON(t, "webauthn.get", challengeB64, origin)

	clientDataHash := sha256.Sum256(cdj)
	sigData := append(append([]byte{}, rawAuthData...), clientDataHash[:]...)
	// ES256 signs the SHA-256 digest of authenticatorData||clientDataHash;
	// go-webauthn hashes this data before ecdsa.VerifyASN1, so we must sign
	// the digest, not the raw bytes.
	digest := sha256.Sum256(sigData)
	sig, err := ecdsa.SignASN1(rand.Reader, v.priv, digest[:])
	if err != nil {
		t.Fatalf("sign assertion: %v", err)
	}

	credIDB64 := base64.RawURLEncoding.EncodeToString(v.credID)
	body := map[string]any{
		"id":    credIDB64,
		"rawId": credIDB64,
		"type":  "public-key",
		"response": map[string]any{
			"authenticatorData": base64.RawURLEncoding.EncodeToString(rawAuthData),
			"clientDataJSON":    base64.RawURLEncoding.EncodeToString(cdj),
			"signature":         base64.RawURLEncoding.EncodeToString(sig),
			"userHandle":        base64.RawURLEncoding.EncodeToString(userHandle),
		},
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal assertion response: %v", err)
	}
	return data
}

// challengeFromOptions extracts the base64url challenge string from a
// begin-ceremony response's "options" field (shared shape for both
// registration and login: {"publicKey":{"challenge":"..."}}).
func challengeFromOptions(t *testing.T, options json.RawMessage) string {
	t.Helper()
	var parsed struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(options, &parsed); err != nil {
		t.Fatalf("parse options: %v", err)
	}
	if parsed.PublicKey.Challenge == "" {
		t.Fatal("expected non-empty challenge in options")
	}
	return parsed.PublicKey.Challenge
}
