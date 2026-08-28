/**
 * Browser-side WebAuthn glue: base64url codecs, the JSON <-> BufferSource
 * conversions the credentials API needs, feature detection, and the error copy.
 *
 * Pure by design — no network, no PocketBase client — so every piece here is
 * unit-testable under plain Node, which matters because this project has no
 * component-testing library and these conversions are the part most likely to
 * break silently.
 */

/**
 * Conversions are done by hand rather than with PublicKeyCredential.toJSON() and
 * parseCreationOptionsFromJSON(). Those are typed in the TS lib this project
 * compiles against, but at runtime they only reached baseline in early 2025, so a
 * self-hosted install facing Firefox ESR or an older iOS would need this manual
 * path as a fallback anyway. One code path that always runs beats two where only
 * one is ever exercised.
 */

const base64ChunkSize = 0x8000

/**
 * Decodes an unpadded (or padded) base64url string.
 *
 * The return type is spelled Uint8Array<ArrayBuffer> rather than plain
 * Uint8Array because the bare name now defaults to ArrayBufferLike, which
 * includes SharedArrayBuffer and so does not satisfy BufferSource.
 */
export function base64UrlToBytes(value: string): Uint8Array<ArrayBuffer> {
  const normalized = value.replace(/\s+/g, '').replace(/-/g, '+').replace(/_/g, '/')
  const padded = normalized.padEnd(normalized.length + ((4 - (normalized.length % 4)) % 4), '=')
  let binary: string
  try {
    binary = atob(padded)
  } catch {
    throw new Error('The server sent a passkey challenge this browser could not read.')
  }
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i)
  }
  return bytes
}

/** Encodes bytes as unpadded base64url, which is what the WebAuthn JSON uses. */
export function bytesToBase64Url(value: ArrayBuffer | Uint8Array): string {
  const bytes = value instanceof Uint8Array ? value : new Uint8Array(value)
  // Chunked: an attestation object runs to several kilobytes and spreading that
  // into String.fromCharCode in one call overflows the argument limit.
  let binary = ''
  for (let offset = 0; offset < bytes.length; offset += base64ChunkSize) {
    binary += String.fromCharCode(...bytes.subarray(offset, offset + base64ChunkSize))
  }
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

type JSONObject = Record<string, unknown>

/**
 * The backend sends what Go's protocol.CredentialCreation serializes to, whose
 * top level is `{"publicKey": {...}}`. Unwrapping defensively means a bare
 * options object would also work.
 */
function unwrapOptions(raw: unknown): JSONObject {
  if (!raw || typeof raw !== 'object') {
    throw new Error('The server sent an unreadable passkey request.')
  }
  const outer = raw as JSONObject
  const inner = outer.publicKey
  if (inner && typeof inner === 'object') {
    return inner as JSONObject
  }
  return outer
}

function requireChallenge(source: JSONObject): Uint8Array<ArrayBuffer> {
  if (typeof source.challenge !== 'string') {
    throw new Error('The server sent a passkey request with no challenge.')
  }
  return base64UrlToBytes(source.challenge)
}

type DescriptorJSON = { id?: unknown; type?: unknown; transports?: unknown }

function toDescriptor(raw: DescriptorJSON): PublicKeyCredentialDescriptor {
  if (typeof raw.id !== 'string') {
    throw new Error('The server sent a passkey credential with no id.')
  }
  const descriptor: PublicKeyCredentialDescriptor = {
    type: 'public-key',
    id: base64UrlToBytes(raw.id),
  }
  if (Array.isArray(raw.transports)) {
    descriptor.transports = raw.transports as AuthenticatorTransport[]
  }
  return descriptor
}

function toDescriptors(raw: unknown): PublicKeyCredentialDescriptor[] | undefined {
  if (!Array.isArray(raw)) {
    return undefined
  }
  return raw.map((entry) => toDescriptor(entry as DescriptorJSON))
}

/**
 * `hints` is a WebAuthn Level 3 member that the non-JSON TS option types do not
 * declare yet. Browsers ignore members they do not implement, so passing it
 * through is safe; widening the type is just to keep tsc honest.
 */
export type CreationOptions = PublicKeyCredentialCreationOptions & { hints?: string[] }
export type RequestOptions = PublicKeyCredentialRequestOptions & { hints?: string[] }

/** Converts the server's registration options into what credentials.create() wants. */
export function toCreationOptions(raw: unknown): CreationOptions {
  const source = unwrapOptions(raw)
  const user = (source.user ?? {}) as JSONObject
  if (typeof user.id !== 'string') {
    throw new Error('The server sent a passkey request with no user handle.')
  }

  const options: CreationOptions = {
    challenge: requireChallenge(source),
    rp: source.rp as PublicKeyCredentialRpEntity,
    user: {
      id: base64UrlToBytes(user.id),
      name: String(user.name ?? ''),
      displayName: String(user.displayName ?? ''),
    },
    pubKeyCredParams: (source.pubKeyCredParams ?? []) as PublicKeyCredentialParameters[],
  }

  const excludeCredentials = toDescriptors(source.excludeCredentials)
  if (excludeCredentials) {
    options.excludeCredentials = excludeCredentials
  }
  if (typeof source.timeout === 'number') {
    options.timeout = source.timeout
  }
  if (source.authenticatorSelection) {
    options.authenticatorSelection = source.authenticatorSelection as AuthenticatorSelectionCriteria
  }
  if (typeof source.attestation === 'string') {
    options.attestation = source.attestation as AttestationConveyancePreference
  }
  if (source.extensions) {
    // The extensions this backend sends (credProps) carry no binary values. If
    // prf or largeBlob are ever added, their inputs arrive base64url and would
    // have to be decoded here.
    options.extensions = source.extensions as AuthenticationExtensionsClientInputs
  }
  if (Array.isArray(source.hints)) {
    options.hints = source.hints as string[]
  }
  return options
}

/** Converts the server's assertion options into what credentials.get() wants. */
export function toRequestOptions(raw: unknown): RequestOptions {
  const source = unwrapOptions(raw)
  const options: RequestOptions = { challenge: requireChallenge(source) }

  // The usernameless flow sends no allowCredentials at all; that absence is what
  // tells the authenticator to offer whatever discoverable credentials it holds.
  const allowCredentials = toDescriptors(source.allowCredentials)
  if (allowCredentials) {
    options.allowCredentials = allowCredentials
  }
  if (typeof source.timeout === 'number') {
    options.timeout = source.timeout
  }
  if (typeof source.rpId === 'string') {
    options.rpId = source.rpId
  }
  if (typeof source.userVerification === 'string') {
    options.userVerification = source.userVerification as UserVerificationRequirement
  }
  if (source.extensions) {
    options.extensions = source.extensions as AuthenticationExtensionsClientInputs
  }
  if (Array.isArray(source.hints)) {
    options.hints = source.hints as string[]
  }
  return options
}

export type RegistrationCredentialJSON = {
  id: string
  rawId: string
  type: string
  authenticatorAttachment?: string
  clientExtensionResults: Record<string, unknown>
  response: { clientDataJSON: string; attestationObject: string; transports?: string[] }
}

export type AuthenticationCredentialJSON = {
  id: string
  rawId: string
  type: string
  authenticatorAttachment?: string
  clientExtensionResults: Record<string, unknown>
  response: {
    clientDataJSON: string
    authenticatorData: string
    signature: string
    userHandle?: string
  }
}

// Duck-typed rather than `instanceof AuthenticatorAttestationResponse`, so these
// converters can be exercised with plain objects in a Node test run. The shape
// check is the part that actually protects the casts below.
type RawResponse = {
  clientDataJSON?: unknown
  attestationObject?: unknown
  authenticatorData?: unknown
  signature?: unknown
  userHandle?: unknown
  getTransports?: () => string[]
}

type RawCredential = {
  id?: unknown
  rawId?: unknown
  type?: unknown
  authenticatorAttachment?: unknown
  response?: RawResponse
  getClientExtensionResults?: () => Record<string, unknown>
}

function credentialShell(credential: RawCredential) {
  if (typeof credential.id !== 'string' || !credential.rawId) {
    throw new Error('The authenticator returned an unreadable passkey.')
  }
  const shell = {
    id: credential.id,
    // rawId as well as id: they carry the same value, and go-webauthn reads rawId.
    rawId: bytesToBase64Url(credential.rawId as ArrayBuffer),
    type: typeof credential.type === 'string' ? credential.type : 'public-key',
    clientExtensionResults:
      typeof credential.getClientExtensionResults === 'function'
        ? credential.getClientExtensionResults()
        : {},
  }
  return typeof credential.authenticatorAttachment === 'string'
    ? { ...shell, authenticatorAttachment: credential.authenticatorAttachment }
    : shell
}

/** Serializes a newly created credential for the register/finish endpoint. */
export function registrationToJSON(credential: unknown): RegistrationCredentialJSON {
  const raw = credential as RawCredential
  const response = raw.response
  if (!response?.clientDataJSON || !response.attestationObject) {
    throw new Error('The authenticator returned an unreadable passkey.')
  }

  const out: RegistrationCredentialJSON = {
    ...credentialShell(raw),
    response: {
      clientDataJSON: bytesToBase64Url(response.clientDataJSON as ArrayBuffer),
      attestationObject: bytesToBase64Url(response.attestationObject as ArrayBuffer),
    },
  }
  // Transports are worth sending — the server persists them and uses them as
  // hints later — but getTransports is absent on older Safari.
  if (typeof response.getTransports === 'function') {
    const transports = response.getTransports()
    if (Array.isArray(transports) && transports.length > 0) {
      out.response.transports = transports
    }
  }
  // authenticatorData, publicKey and publicKeyAlgorithm are deliberately left
  // out: go-webauthn recovers all three from the attestation object, and the
  // getters for them throw on some older implementations. Do not "fix" this by
  // adding them back.
  return out
}

/** Serializes an assertion for the login/finish endpoint. */
export function assertionToJSON(credential: unknown): AuthenticationCredentialJSON {
  const raw = credential as RawCredential
  const response = raw.response
  if (!response?.clientDataJSON || !response.authenticatorData || !response.signature) {
    throw new Error('The authenticator returned an unreadable passkey.')
  }

  const out: AuthenticationCredentialJSON = {
    ...credentialShell(raw),
    response: {
      clientDataJSON: bytesToBase64Url(response.clientDataJSON as ArrayBuffer),
      authenticatorData: bytesToBase64Url(response.authenticatorData as ArrayBuffer),
      signature: bytesToBase64Url(response.signature as ArrayBuffer),
    },
  }
  // The user handle is how a discoverable login names the account, so it is sent
  // when present and omitted rather than nulled when it is not.
  if (response.userHandle) {
    out.response.userHandle = bytesToBase64Url(response.userHandle as ArrayBuffer)
  }
  return out
}

function hostnameIsIPAddress(hostname: string): boolean {
  if (/^\d{1,3}(\.\d{1,3}){3}$/.test(hostname)) {
    return true
  }
  // A bracketed IPv6 literal, as location.hostname reports it.
  return hostname.startsWith('[') && hostname.endsWith(']')
}

/**
 * True when a ceremony can actually run here: the API exists, the page is a
 * secure context, and the origin is not an IP address.
 *
 * The IP check is not pedantry — it is the single most common self-hosting
 * mistake. A passkey is bound to a registrable domain, so an IP literal can
 * never be a relying-party ID, and that holds even over HTTPS.
 */
export function passkeysSupported(): boolean {
  if (typeof window === 'undefined' || typeof navigator === 'undefined') {
    return false
  }
  if (typeof window.PublicKeyCredential !== 'function') {
    return false
  }
  if (typeof navigator.credentials?.get !== 'function') {
    return false
  }
  if (!window.isSecureContext) {
    return false
  }
  return !hostnameIsIPAddress(window.location.hostname)
}

/** Explains why passkeys are unavailable. Empty string when they are available. */
export function passkeyUnavailableHint(): string {
  if (typeof window === 'undefined' || typeof window.PublicKeyCredential !== 'function') {
    return 'This browser does not support passkeys.'
  }
  if (!window.isSecureContext) {
    return 'Passkeys need a secure connection. Open this app over HTTPS, or from localhost.'
  }
  if (hostnameIsIPAddress(window.location.hostname)) {
    return 'Passkeys cannot be used on an IP address. Open this app by its hostname (for example http://localhost:8090 or https://archive.example.com).'
  }
  return ''
}

/** Whether the browser can offer a passkey from the email field's autofill. */
export async function conditionalMediationAvailable(): Promise<boolean> {
  if (!passkeysSupported()) {
    return false
  }
  const api = window.PublicKeyCredential
  if (typeof api.isConditionalMediationAvailable !== 'function') {
    return false
  }
  try {
    return await api.isConditionalMediationAvailable()
  } catch {
    return false
  }
}

/** Whether this device has a built-in authenticator (Touch ID, Windows Hello, ...). */
export async function platformAuthenticatorAvailable(): Promise<boolean> {
  if (!passkeysSupported()) {
    return false
  }
  const api = window.PublicKeyCredential
  if (typeof api.isUserVerifyingPlatformAuthenticatorAvailable !== 'function') {
    return false
  }
  try {
    return await api.isUserVerifyingPlatformAuthenticatorAvailable()
  } catch {
    return false
  }
}

export type PasskeyCeremony = 'login' | 'register'

/** True for the abort we triggered ourselves, which must never surface as an error. */
export function isAbortError(err: unknown): boolean {
  return err instanceof DOMException && err.name === 'AbortError'
}

/**
 * Maps a ceremony failure to something worth reading, following the precedent of
 * oauthErrorMessage in auth.ts: the browser's DOMException names are accurate but
 * meaningless to the person looking at them.
 */
export function passkeyErrorMessage(err: unknown, ceremony: PasskeyCeremony): string {
  const registering = ceremony === 'register'

  if (err instanceof DOMException) {
    switch (err.name) {
      case 'NotAllowedError':
        return registering
          ? 'Passkey setup was cancelled or timed out. Nothing was saved.'
          : 'Passkey sign-in was cancelled or timed out. Try again, or sign in with your email and password.'
      case 'AbortError':
        return 'Passkey sign-in was cancelled.'
      case 'InvalidStateError':
        return 'This device already has a passkey for this account. Use it to sign in, or remove the old one first.'
      case 'SecurityError':
        return 'Passkeys need a secure connection to a hostname. A plain-HTTP page, or an address like http://192.168.1.10, cannot use them.'
      case 'NotSupportedError':
        return 'This device could not create a passkey of a supported type.'
      case 'ConstraintError':
        return 'This device cannot create a passkey yet — a screen lock or biometric may need to be set up first.'
      case 'UnknownError':
        return 'The authenticator could not complete the request. Try again.'
    }
  }

  if (err instanceof Error && err.message) {
    // PocketBase reports a rejected session as a bare "Failed to authenticate.",
    // which here almost always means the credential outlived its user record.
    if (err.message === 'Failed to authenticate.') {
      return 'That passkey is no longer valid on this server. Sign in another way, then remove and add it again.'
    }
    // Otherwise this is the server's own {"detail": ...}; it owns the wording.
    return err.message
  }

  return registering ? 'Passkey setup failed' : 'Passkey sign-in failed'
}

const browserTokens: [RegExp, string][] = [
  [/Edg\//, 'Edge'],
  [/OPR\/|Opera/, 'Opera'],
  [/Chrome\//, 'Chrome'],
  [/Firefox\//, 'Firefox'],
  [/Safari\//, 'Safari'],
]

const platformTokens: [RegExp, string][] = [
  [/iPhone/, 'iPhone'],
  [/iPad/, 'iPad'],
  [/Android/, 'Android'],
  [/Macintosh|Mac OS X/, 'Mac'],
  [/Windows/, 'Windows'],
  [/Linux/, 'Linux'],
]

function firstMatch(tokens: [RegExp, string][], value: string): string {
  for (const [pattern, label] of tokens) {
    if (pattern.test(value)) {
      return label
    }
  }
  return ''
}

/**
 * A guessed label for a new passkey, so the name field is never empty. Takes the
 * user agent as an argument purely so it can be tested.
 */
export function defaultPasskeyName(userAgent?: string): string {
  const ua = userAgent ?? (typeof navigator === 'undefined' ? '' : navigator.userAgent)
  if (!ua) {
    return 'Passkey'
  }
  const browser = firstMatch(browserTokens, ua)
  const platform = firstMatch(platformTokens, ua)
  if (browser && platform) {
    return `${browser} on ${platform}`
  }
  return browser || platform || 'Passkey'
}
