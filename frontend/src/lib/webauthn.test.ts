import { describe, expect, it } from 'vitest'
import {
  assertionToJSON,
  base64UrlToBytes,
  bytesToBase64Url,
  defaultPasskeyName,
  isAbortError,
  passkeyErrorMessage,
  passkeysSupported,
  registrationToJSON,
  toCreationOptions,
  toRequestOptions,
} from './webauthn'

function bytes(...values: number[]) {
  return new Uint8Array(values)
}

describe('base64url codecs', () => {
  it('round-trips bytes that use the url-safe alphabet', () => {
    // These encode to '+' and '/' in standard base64. Getting the swap wrong in
    // either direction means a credential can never be found again at login.
    const raw = bytes(0xfb, 0xff, 0xbe, 0x00, 0x01)
    const encoded = bytesToBase64Url(raw)

    expect(encoded).not.toMatch(/[+/=]/)
    expect(base64UrlToBytes(encoded)).toEqual(raw)
  })

  it('handles every padding remainder', () => {
    for (const length of [1, 2, 3, 4, 5]) {
      const raw = new Uint8Array(length).fill(0x41)
      expect(base64UrlToBytes(bytesToBase64Url(raw))).toEqual(raw)
    }
  })

  it('emits no padding but accepts padded input', () => {
    const raw = bytes(1, 2, 3, 4, 5)
    const unpadded = bytesToBase64Url(raw)
    expect(unpadded.endsWith('=')).toBe(false)

    const padded = unpadded.padEnd(unpadded.length + ((4 - (unpadded.length % 4)) % 4), '=')
    expect(base64UrlToBytes(padded)).toEqual(raw)
  })

  it('round-trips an empty value', () => {
    expect(bytesToBase64Url(bytes())).toBe('')
    expect(base64UrlToBytes('')).toEqual(bytes())
  })

  it('encodes a buffer larger than one chunk', () => {
    // An attestation object runs to several kilobytes; spreading that into
    // String.fromCharCode in one call overflows the argument limit.
    const raw = new Uint8Array(100_000)
    for (let i = 0; i < raw.length; i++) {
      raw[i] = i % 256
    }
    expect(base64UrlToBytes(bytesToBase64Url(raw))).toEqual(raw)
  })

  it('accepts an ArrayBuffer as well as a view', () => {
    const raw = bytes(9, 8, 7)
    expect(bytesToBase64Url(raw.buffer as ArrayBuffer)).toBe(bytesToBase64Url(raw))
  })

  it('reports malformed input as a readable error', () => {
    expect(() => base64UrlToBytes('!!!!')).toThrow(/could not read/i)
  })
})

describe('toCreationOptions', () => {
  const raw = {
    publicKey: {
      challenge: 'AQIDBA',
      rp: { name: 'Lemmary', id: 'archive.example.com' },
      user: { id: 'BQYHCA', name: 'a@example.com', displayName: 'Ada' },
      pubKeyCredParams: [{ type: 'public-key', alg: -7 }],
      timeout: 60000,
      attestation: 'none',
      authenticatorSelection: { residentKey: 'required', userVerification: 'preferred' },
      excludeCredentials: [{ type: 'public-key', id: 'CQoLDA', transports: ['internal'] }],
      extensions: { credProps: true },
      hints: ['client-device'],
    },
  }

  it('decodes every buffer field and passes the rest through', () => {
    const options = toCreationOptions(raw)

    expect(options.challenge).toEqual(bytes(1, 2, 3, 4))
    expect(options.user.id).toEqual(bytes(5, 6, 7, 8))
    expect(options.excludeCredentials?.[0].id).toEqual(bytes(9, 10, 11, 12))
    expect(options.excludeCredentials?.[0].transports).toEqual(['internal'])
    expect(options.rp).toEqual({ name: 'Lemmary', id: 'archive.example.com' })
    expect(options.user.name).toBe('a@example.com')
    expect(options.user.displayName).toBe('Ada')
    expect(options.pubKeyCredParams).toEqual([{ type: 'public-key', alg: -7 }])
    expect(options.timeout).toBe(60000)
    expect(options.attestation).toBe('none')
    expect(options.authenticatorSelection).toEqual({
      residentKey: 'required',
      userVerification: 'preferred',
    })
    expect(options.extensions).toEqual({ credProps: true })
    expect(options.hints).toEqual(['client-device'])
  })

  it('accepts a bare options object as well as the publicKey envelope', () => {
    const options = toCreationOptions(raw.publicKey)
    expect(options.challenge).toEqual(bytes(1, 2, 3, 4))
  })

  it('omits excludeCredentials entirely when the server sent none', () => {
    const rest = { ...raw.publicKey }
    delete (rest as Record<string, unknown>).excludeCredentials
    const options = toCreationOptions({ publicKey: rest })
    expect('excludeCredentials' in options).toBe(false)
  })

  it('rejects a payload with no challenge or no user handle', () => {
    const noChallenge = { ...raw.publicKey }
    delete (noChallenge as Record<string, unknown>).challenge
    expect(() => toCreationOptions({ publicKey: noChallenge })).toThrow(/challenge/i)

    expect(() =>
      toCreationOptions({ publicKey: { ...raw.publicKey, user: { name: 'a', displayName: 'a' } } }),
    ).toThrow(/user handle/i)
  })

  it('rejects a descriptor with no id', () => {
    expect(() =>
      toCreationOptions({
        publicKey: { ...raw.publicKey, excludeCredentials: [{ type: 'public-key' }] },
      }),
    ).toThrow(/id/i)
  })
})

describe('toRequestOptions', () => {
  it('decodes the challenge and passes the rest through', () => {
    const options = toRequestOptions({
      publicKey: {
        challenge: 'AQIDBA',
        timeout: 60000,
        rpId: 'archive.example.com',
        userVerification: 'preferred',
        extensions: { credProps: true },
        hints: ['security-key'],
      },
    })

    expect(options.challenge).toEqual(bytes(1, 2, 3, 4))
    expect(options.timeout).toBe(60000)
    expect(options.rpId).toBe('archive.example.com')
    expect(options.userVerification).toBe('preferred')
    expect(options.extensions).toEqual({ credProps: true })
    expect(options.hints).toEqual(['security-key'])
  })

  it('omits allowCredentials for the usernameless flow', () => {
    // The absence of the list is what tells the authenticator to offer whatever
    // discoverable credentials it holds.
    const options = toRequestOptions({ publicKey: { challenge: 'AQIDBA' } })
    expect('allowCredentials' in options).toBe(false)
  })

  it('decodes allowCredentials when the server does send them', () => {
    const options = toRequestOptions({
      publicKey: {
        challenge: 'AQIDBA',
        allowCredentials: [{ type: 'public-key', id: 'CQoLDA' }],
      },
    })
    expect(options.allowCredentials?.[0].id).toEqual(bytes(9, 10, 11, 12))
  })

  it('rejects a payload that is not an object', () => {
    expect(() => toRequestOptions(null)).toThrow(/unreadable/i)
    expect(() => toRequestOptions('nope')).toThrow(/unreadable/i)
  })
})

// Plain objects rather than real credentials: the converters are duck-typed on
// purpose so they can be exercised without a browser.
function fakeRegistration(overrides: Record<string, unknown> = {}) {
  return {
    id: 'Y3JlZC1pZA',
    rawId: bytes(1, 2, 3).buffer,
    type: 'public-key',
    authenticatorAttachment: 'platform',
    response: {
      clientDataJSON: bytes(4, 5, 6).buffer,
      attestationObject: bytes(7, 8, 9).buffer,
      getTransports: () => ['internal', 'hybrid'],
    },
    getClientExtensionResults: () => ({ credProps: { rk: true } }),
    ...overrides,
  }
}

describe('registrationToJSON', () => {
  it('encodes the buffers and keeps the identifying fields', () => {
    const json = registrationToJSON(fakeRegistration())

    expect(json.id).toBe('Y3JlZC1pZA')
    expect(json.rawId).toBe(bytesToBase64Url(bytes(1, 2, 3)))
    expect(json.type).toBe('public-key')
    expect(json.authenticatorAttachment).toBe('platform')
    expect(json.clientExtensionResults).toEqual({ credProps: { rk: true } })
    expect(json.response.clientDataJSON).toBe(bytesToBase64Url(bytes(4, 5, 6)))
    expect(json.response.attestationObject).toBe(bytesToBase64Url(bytes(7, 8, 9)))
    expect(json.response.transports).toEqual(['internal', 'hybrid'])
  })

  it('omits the fields the server recomputes from the attestation object', () => {
    // Sending these back is not just wasteful; the getters throw on some older
    // implementations. Pinned so nobody "fixes" it by adding them.
    const json = registrationToJSON(fakeRegistration()) as Record<string, unknown>
    const response = json.response as Record<string, unknown>
    expect('authenticatorData' in response).toBe(false)
    expect('publicKey' in response).toBe(false)
    expect('publicKeyAlgorithm' in response).toBe(false)
  })

  it('copes with an authenticator that has no getTransports', () => {
    const credential = fakeRegistration({
      response: {
        clientDataJSON: bytes(4, 5, 6).buffer,
        attestationObject: bytes(7, 8, 9).buffer,
      },
    })
    const json = registrationToJSON(credential)
    expect('transports' in json.response).toBe(false)
  })

  it('omits authenticatorAttachment when the browser did not report one', () => {
    const credential = fakeRegistration({ authenticatorAttachment: null })
    expect('authenticatorAttachment' in registrationToJSON(credential)).toBe(false)
  })

  it('rejects a response with nothing usable in it', () => {
    expect(() => registrationToJSON({ id: 'x', rawId: bytes(1).buffer, response: {} })).toThrow(
      /unreadable/i,
    )
  })
})

function fakeAssertion(overrides: Record<string, unknown> = {}) {
  return {
    id: 'Y3JlZC1pZA',
    rawId: bytes(1, 2, 3).buffer,
    type: 'public-key',
    response: {
      clientDataJSON: bytes(4, 5, 6).buffer,
      authenticatorData: bytes(7, 8, 9).buffer,
      signature: bytes(10, 11, 12).buffer,
      userHandle: bytes(13, 14).buffer,
    },
    getClientExtensionResults: () => ({}),
    ...overrides,
  }
}

describe('assertionToJSON', () => {
  it('encodes every buffer including the user handle', () => {
    const json = assertionToJSON(fakeAssertion())

    expect(json.rawId).toBe(bytesToBase64Url(bytes(1, 2, 3)))
    expect(json.response.clientDataJSON).toBe(bytesToBase64Url(bytes(4, 5, 6)))
    expect(json.response.authenticatorData).toBe(bytesToBase64Url(bytes(7, 8, 9)))
    expect(json.response.signature).toBe(bytesToBase64Url(bytes(10, 11, 12)))
    expect(json.response.userHandle).toBe(bytesToBase64Url(bytes(13, 14)))
  })

  it('omits a null user handle rather than sending null', () => {
    // The user handle is how a discoverable login names the account, so both
    // branches matter.
    const credential = fakeAssertion({
      response: {
        clientDataJSON: bytes(4, 5, 6).buffer,
        authenticatorData: bytes(7, 8, 9).buffer,
        signature: bytes(10, 11, 12).buffer,
        userHandle: null,
      },
    })
    const json = assertionToJSON(credential)
    expect('userHandle' in json.response).toBe(false)
  })

  it('rejects an assertion with no signature', () => {
    const credential = fakeAssertion({
      response: {
        clientDataJSON: bytes(4, 5, 6).buffer,
        authenticatorData: bytes(7, 8, 9).buffer,
      },
    })
    expect(() => assertionToJSON(credential)).toThrow(/unreadable/i)
  })
})

describe('passkeyErrorMessage', () => {
  function domError(name: string) {
    return new DOMException('', name)
  }

  it('distinguishes cancelling a sign-in from cancelling setup', () => {
    const login = passkeyErrorMessage(domError('NotAllowedError'), 'login')
    const register = passkeyErrorMessage(domError('NotAllowedError'), 'register')

    expect(login).toMatch(/cancelled or timed out/i)
    expect(login).toMatch(/email and password/i)
    expect(register).toMatch(/nothing was saved/i)
  })

  it('explains a duplicate enrollment', () => {
    expect(passkeyErrorMessage(domError('InvalidStateError'), 'register')).toMatch(
      /already has a passkey/i,
    )
  })

  it('explains an insecure context', () => {
    expect(passkeyErrorMessage(domError('SecurityError'), 'login')).toMatch(/secure connection/i)
  })

  it('covers the remaining DOMException names', () => {
    expect(passkeyErrorMessage(domError('NotSupportedError'), 'register')).toMatch(/supported type/i)
    expect(passkeyErrorMessage(domError('ConstraintError'), 'register')).toMatch(/screen lock/i)
    expect(passkeyErrorMessage(domError('UnknownError'), 'login')).toMatch(/could not complete/i)
    expect(passkeyErrorMessage(domError('AbortError'), 'login')).toMatch(/cancelled/i)
  })

  it('rewrites PocketBase’s bare authentication failure', () => {
    // Here it nearly always means the credential outlived its user record.
    expect(passkeyErrorMessage(new Error('Failed to authenticate.'), 'login')).toMatch(
      /no longer valid/i,
    )
  })

  it('passes a server detail message through unchanged', () => {
    const detail = 'Passkeys need a hostname, not an IP address.'
    expect(passkeyErrorMessage(new Error(detail), 'login')).toBe(detail)
  })

  it('falls back per ceremony for anything unrecognizable', () => {
    expect(passkeyErrorMessage({ nope: true }, 'login')).toBe('Passkey sign-in failed')
    expect(passkeyErrorMessage({ nope: true }, 'register')).toBe('Passkey setup failed')
  })
})

describe('isAbortError', () => {
  it('matches only the abort we triggered ourselves', () => {
    expect(isAbortError(new DOMException('', 'AbortError'))).toBe(true)
    expect(isAbortError(new DOMException('', 'NotAllowedError'))).toBe(false)
    expect(isAbortError(new Error('AbortError'))).toBe(false)
  })
})

describe('passkeysSupported', () => {
  it('is false with no window, which is what makes this module testable', () => {
    expect(passkeysSupported()).toBe(false)
  })
})

describe('defaultPasskeyName', () => {
  it('names the browser and the platform', () => {
    const cases: [string, string][] = [
      [
        'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36 Edg/141.0.0.0',
        'Edge on Windows',
      ],
      [
        'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36',
        'Chrome on Mac',
      ],
      [
        'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1',
        'Safari on iPhone',
      ],
      ['Mozilla/5.0 (X11; Linux x86_64; rv:135.0) Gecko/20100101 Firefox/135.0', 'Firefox on Linux'],
      ['Mozilla/5.0 (Linux; Android 15) AppleWebKit/537.36 Chrome/141.0.0.0', 'Chrome on Android'],
    ]
    for (const [ua, want] of cases) {
      expect(defaultPasskeyName(ua)).toBe(want)
    }
  })

  it('falls back when the user agent says nothing useful', () => {
    expect(defaultPasskeyName('')).toBe('Passkey')
    expect(defaultPasskeyName('curl/8.0')).toBe('Passkey')
  })
})
