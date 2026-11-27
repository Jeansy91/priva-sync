import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  passkeysSupported,
  createPasskey,
  getPasskey,
  PasskeyNotSupportedError,
  PasskeyCancelledError,
} from './webauthn'

const originalPublicKeyCredential = (window as any).PublicKeyCredential
const originalCredentials = (navigator as any).credentials

afterEach(() => {
  ;(window as any).PublicKeyCredential = originalPublicKeyCredential
  Object.defineProperty(navigator, 'credentials', { value: originalCredentials, configurable: true })
  vi.restoreAllMocks()
})

// Tests only care about pass-through/unwrap behavior, not the full WebAuthn
// options shape -- cast minimal fixtures rather than filling every required
// DOM field.
function creationOptions(partial: Partial<PublicKeyCredentialCreationOptionsJSON>) {
  return { publicKey: partial as PublicKeyCredentialCreationOptionsJSON }
}
function requestOptions(partial: Partial<PublicKeyCredentialRequestOptionsJSON>) {
  return { publicKey: partial as PublicKeyCredentialRequestOptionsJSON }
}

// jsdom does not implement the Credential Management API, so
// navigator.credentials is undefined by default -- stub it per test.
function mockSupported() {
  ;(window as any).PublicKeyCredential = {
    parseCreationOptionsFromJSON: vi.fn((o: unknown) => o),
    parseRequestOptionsFromJSON: vi.fn((o: unknown) => o),
  }
  Object.defineProperty(navigator, 'credentials', {
    value: { create: vi.fn(), get: vi.fn() },
    configurable: true,
  })
}

describe('passkeysSupported', () => {
  it('is false when window.PublicKeyCredential is undefined', () => {
    ;(window as any).PublicKeyCredential = undefined
    expect(passkeysSupported()).toBe(false)
  })

  it('is true when window.PublicKeyCredential exists', () => {
    mockSupported()
    expect(passkeysSupported()).toBe(true)
  })
})

describe('createPasskey', () => {
  it('throws PasskeyNotSupportedError when the browser lacks support', async () => {
    ;(window as any).PublicKeyCredential = undefined
    await expect(createPasskey(creationOptions({}))).rejects.toBeInstanceOf(PasskeyNotSupportedError)
  })

  it('unwraps the server\'s publicKey wrapper before parsing', async () => {
    mockSupported()
    navigator.credentials.create = vi.fn().mockResolvedValue({ toJSON: () => ({ id: 'cred-1' }) })

    await createPasskey(creationOptions({ challenge: 'abc' }))

    expect(window.PublicKeyCredential.parseCreationOptionsFromJSON).toHaveBeenCalledWith({ challenge: 'abc' })
  })

  it('returns credential.toJSON() on success', async () => {
    mockSupported()
    const toJSON = vi.fn(() => ({ id: 'cred-1' }))
    navigator.credentials.create = vi.fn().mockResolvedValue({ toJSON })

    const result = await createPasskey(creationOptions({ challenge: 'abc' }))

    expect(result).toEqual({ id: 'cred-1' })
    expect(navigator.credentials.create).toHaveBeenCalled()
  })

  it('throws PasskeyCancelledError when the credential is null', async () => {
    mockSupported()
    navigator.credentials.create = vi.fn().mockResolvedValue(null)
    await expect(createPasskey(creationOptions({}))).rejects.toBeInstanceOf(PasskeyCancelledError)
  })

  it('maps a NotAllowedError DOMException to PasskeyCancelledError', async () => {
    mockSupported()
    navigator.credentials.create = vi.fn().mockRejectedValue(new DOMException('nope', 'NotAllowedError'))
    await expect(createPasskey(creationOptions({}))).rejects.toBeInstanceOf(PasskeyCancelledError)
  })

  it('rethrows other errors unchanged', async () => {
    mockSupported()
    navigator.credentials.create = vi.fn().mockRejectedValue(new Error('boom'))
    await expect(createPasskey(creationOptions({}))).rejects.toThrow('boom')
  })
})

describe('getPasskey', () => {
  it('throws PasskeyNotSupportedError when the browser lacks support', async () => {
    ;(window as any).PublicKeyCredential = undefined
    await expect(getPasskey(requestOptions({}))).rejects.toBeInstanceOf(PasskeyNotSupportedError)
  })

  it('unwraps the server\'s publicKey wrapper before parsing', async () => {
    mockSupported()
    navigator.credentials.get = vi.fn().mockResolvedValue({ toJSON: () => ({ id: 'assertion-1' }) })

    await getPasskey(requestOptions({ challenge: 'abc' }))

    expect(window.PublicKeyCredential.parseRequestOptionsFromJSON).toHaveBeenCalledWith({ challenge: 'abc' })
  })

  it('returns credential.toJSON() on success', async () => {
    mockSupported()
    const toJSON = vi.fn(() => ({ id: 'assertion-1' }))
    navigator.credentials.get = vi.fn().mockResolvedValue({ toJSON })

    const result = await getPasskey(requestOptions({ challenge: 'abc' }))

    expect(result).toEqual({ id: 'assertion-1' })
  })

  it('throws PasskeyCancelledError when the credential is null', async () => {
    mockSupported()
    navigator.credentials.get = vi.fn().mockResolvedValue(null)
    await expect(getPasskey(requestOptions({}))).rejects.toBeInstanceOf(PasskeyCancelledError)
  })
})
