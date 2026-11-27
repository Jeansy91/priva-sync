// Thin wrapper around the browser's native WebAuthn API. Uses the
// PublicKeyCredential JSON serialization methods (parseCreationOptionsFromJSON,
// parseRequestOptionsFromJSON, toJSON) so options coming from the server and
// responses going back to it can travel as plain JSON without hand-rolled
// base64url<->ArrayBuffer conversion.

export class PasskeyNotSupportedError extends Error {
  constructor() {
    super('Passkeys are not supported in this browser')
    this.name = 'PasskeyNotSupportedError'
  }
}

export class PasskeyCancelledError extends Error {
  constructor() {
    super('Passkey ceremony was cancelled')
    this.name = 'PasskeyCancelledError'
  }
}

export function passkeysSupported(): boolean {
  return typeof window !== 'undefined' && typeof window.PublicKeyCredential !== 'undefined'
}

function toPasskeyError(err: unknown): Error {
  if (err instanceof DOMException && err.name === 'NotAllowedError') {
    return new PasskeyCancelledError()
  }
  return err instanceof Error ? err : new Error(String(err))
}

// Runs a registration ceremony (navigator.credentials.create) against the
// server's PublicKeyCredentialCreationOptionsJSON and returns the resulting
// credential as JSON, ready to send back to /auth/passkey/register/finish.
//
// The server marshals go-webauthn's CredentialCreation as
// `{ publicKey: PublicKeyCredentialCreationOptionsJSON }` (the shape
// navigator.credentials.create() itself expects) -- unwrap that layer before
// handing the inner options to parseCreationOptionsFromJSON, which wants the
// flat options object.
export async function createPasskey(
  optionsJSON: { publicKey: PublicKeyCredentialCreationOptionsJSON },
): Promise<unknown> {
  if (!passkeysSupported()) throw new PasskeyNotSupportedError()

  const options = PublicKeyCredential.parseCreationOptionsFromJSON(optionsJSON.publicKey)

  let credential: Credential | null
  try {
    credential = await navigator.credentials.create({ publicKey: options })
  } catch (err) {
    throw toPasskeyError(err)
  }
  if (!credential) throw new PasskeyCancelledError()

  return (credential as PublicKeyCredential).toJSON()
}

// Runs a login ceremony (navigator.credentials.get) against the server's
// PublicKeyCredentialRequestOptionsJSON and returns the resulting assertion
// as JSON, ready to send back to /auth/passkey/login/finish.
//
// Same publicKey-wrapper unwrap as createPasskey -- see comment there.
export async function getPasskey(
  optionsJSON: { publicKey: PublicKeyCredentialRequestOptionsJSON },
): Promise<unknown> {
  if (!passkeysSupported()) throw new PasskeyNotSupportedError()

  const options = PublicKeyCredential.parseRequestOptionsFromJSON(optionsJSON.publicKey)

  let credential: Credential | null
  try {
    credential = await navigator.credentials.get({ publicKey: options })
  } catch (err) {
    throw toPasskeyError(err)
  }
  if (!credential) throw new PasskeyCancelledError()

  return (credential as PublicKeyCredential).toJSON()
}
