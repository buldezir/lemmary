import { ClientResponseError, type RecordModel } from 'pocketbase'
import { pb, pbUrl } from './pb'
import {
  assertionToJSON,
  conditionalMediationAvailable,
  isAbortError,
  passkeyErrorMessage,
  passkeysSupported,
  passkeyUnavailableHint,
  toRequestOptions,
} from './webauthn'

export class AuthRequiredError extends Error {
  constructor() {
    super('Authentication required')
    this.name = 'AuthRequiredError'
  }
}

// Dev auto-login credentials are only honored in dev builds, so a production
// bundle can never ship with a working login baked in.
function devCredentials() {
  if (!import.meta.env.DEV) {
    return null
  }
  const email = import.meta.env.VITE_DEV_USER_EMAIL
  const password = import.meta.env.VITE_DEV_USER_PASSWORD
  return email && password ? { email, password } : null
}

export async function ensureAuth() {
  if (pb.authStore.isValid) {
    return
  }

  const dev = devCredentials()
  if (!dev) {
    throw new AuthRequiredError()
  }

  try {
    await pb.collection('users').authWithPassword(dev.email, dev.password)
  } catch {
    throw new AuthRequiredError()
  }
}

export type OAuthProvider = {
  name: string
  displayName: string
}

export type LoginMethods = {
  /** Whether the users collection still accepts identity + password sign-in. */
  password: boolean
  /** OAuth2 providers enabled on the users collection, in PocketBase order. */
  oauth: OAuthProvider[]
  /** Whether the server can and should offer passkey sign-in. */
  passkey: boolean
}

const fallbackLoginMethods: LoginMethods = { password: true, oauth: [], passkey: false }

/**
 * Whether the server says passkey sign-in is on offer: the address can carry a
 * ceremony at all, and at least one credential exists to offer.
 *
 * Read here rather than from the app-meta hook because that hook resolves once
 * per RootLayout mount, and RootLayout does not remount when a session ends. A
 * user who enrolled a passkey and then signed out in the same tab would be shown
 * a login screen built from a stale answer. This runs whenever the login screen
 * appears.
 *
 * Raw fetch, like the two calls below: apiClient imports ensureAuth from this
 * module, so importing it back would close a cycle.
 */
async function getPasskeyOffered(): Promise<boolean> {
  try {
    const response = await fetch(`${pbUrl}/api/app/meta`)
    if (!response.ok) {
      return false
    }
    const data = (await response.json()) as { passkeys?: boolean }
    return data.passkeys === true
  } catch {
    return false
  }
}

/**
 * Reads the sign-in methods enabled for the users collection in PocketBase, so
 * the login screen offers whatever the instance was configured with. The
 * endpoint is public, which is what lets this resolve before any session
 * exists. Never throws: an unreachable server falls back to the password form
 * rather than a login screen with no way in.
 */
export async function getLoginMethods(): Promise<LoginMethods> {
  const [methods, passkey] = await Promise.all([
    pb
      .collection('users')
      .listAuthMethods()
      .catch(() => null),
    getPasskeyOffered(),
  ])
  if (!methods) {
    return { ...fallbackLoginMethods, passkey }
  }

  const oauth = methods.oauth2.enabled
    ? methods.oauth2.providers.map((provider) => ({
        name: provider.name,
        displayName: provider.displayName || provider.name,
      }))
    : []

  return {
    // Hiding the password form is only safe while another method can take over;
    // with nothing left it would be a login screen with no controls at all.
    password: methods.password.enabled || (oauth.length === 0 && !passkey),
    oauth,
    passkey,
  }
}

const oauthPopupName = 'lemmary-oauth2-login'
const oauthPopupFeatures = 'width=600,height=720,menubar=no,toolbar=no,resizable'

/**
 * Opens the popup up front, while still inside the click handler's task. The
 * SDK opens its own popup only after awaiting the auth-methods request and the
 * realtime subscription, which browsers score as an unrequested popup and
 * block.
 */
function openOAuthPopup(): Window | null {
  if (typeof window === 'undefined' || !window.open) {
    return null
  }
  return window.open('', oauthPopupName, oauthPopupFeatures)
}

/**
 * Rejects once the user closes the popup without finishing, since the SDK's
 * promise would otherwise stay pending forever and leave the button spinning.
 */
function watchOAuthPopup(popup: Window | null) {
  let poll = 0
  let grace = 0
  const closed = new Promise<never>((_, reject) => {
    if (!popup) {
      return // no handle to watch; the SDK promise is the only outcome
    }
    poll = window.setInterval(() => {
      if (!popup.closed) {
        return
      }
      window.clearInterval(poll)
      // The redirect page can close itself on success, so let an in-flight
      // token exchange finish before calling this a cancellation.
      grace = window.setTimeout(() => reject(new Error('Sign-in was cancelled.')), 2000)
    }, 400)
  })
  return {
    closed,
    stop() {
      window.clearInterval(poll)
      window.clearTimeout(grace)
    },
  }
}

/**
 * PocketBase reports every sign-in failure past the token exchange as a bare
 * "Failed to authenticate.". In this app that is nearly always a first-time
 * OAuth2 account: the users collection has no create rule, so OAuth2 can sign
 * in accounts that already exist (matched on email) but cannot mint new ones.
 */
function oauthErrorMessage(err: unknown): string {
  if (err instanceof ClientResponseError && err.message === 'Failed to authenticate.') {
    return 'Failed to authenticate. If this account is new, an admin has to create a user with the same email address first.'
  }
  if (err instanceof Error && err.message) {
    return err.message
  }
  return 'Sign-in failed'
}

/** Signs in through one of the collection's OAuth2 providers. */
export async function loginWithOAuth2(provider: string) {
  clearMeCache()
  const popup = openOAuthPopup()
  const watcher = watchOAuthPopup(popup)

  try {
    await Promise.race([
      pb.collection('users').authWithOAuth2({
        provider,
        urlCallback: (url) => {
          if (popup && !popup.closed) {
            popup.location.href = url
            return
          }
          window.open(url, oauthPopupName, oauthPopupFeatures)
        },
      }),
      watcher.closed,
    ])
  } catch (err) {
    throw new Error(oauthErrorMessage(err), { cause: err })
  } finally {
    watcher.stop()
    popup?.close()
  }
}

export async function loginWithPassword(email: string, password: string) {
  clearMeCache()
  try {
    await pb.collection('users').authWithPassword(email, password)
    return
  } catch (err) {
    // Only an auth rejection falls through to the superuser path (legacy
    // installs / PocketBase admin accounts). Network and server errors surface
    // directly instead of triggering a second doomed attempt.
    if (!(err instanceof ClientResponseError) || err.status !== 400) {
      throw err
    }
  }

  await pb.collection('_superusers').authWithPassword(email, password)
  const response = await fetch(`${pbUrl}/api/app/ensure-user`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: pb.authStore.token,
    },
    body: JSON.stringify({ password }),
  })
  const data = (await response.json()) as { detail?: string }
  if (!response.ok) {
    pb.authStore.clear()
    throw new Error(data.detail ?? 'Failed to create paired user account')
  }
  // App sessions must be users-collection so documents.user relations validate.
  await pb.collection('users').authWithPassword(email, password)
}

type PasskeyBeginResponse = { session_id: string; options: unknown }
type PasskeyAuthResponse = { token: string; record: RecordModel }

/**
 * The two passkey login endpoints are called with a raw fetch rather than
 * apiFetch, matching the `ensure-user` and `getMe` calls below. Not a style
 * choice: apiClient.ts imports ensureAuth from this module, so importing it back
 * would close a cycle.
 */
async function postPasskeyPublic<T>(path: string, body: unknown, fallback: string): Promise<T> {
  const response = await fetch(`${pbUrl}${path}`, {
    method: 'POST',
    // The Content-Type matters on the server side: PocketBase's BindBody only
    // decodes (and, crucially, rewinds) a body that declares itself as JSON.
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  const data = (await response.json().catch(() => null)) as (T & { detail?: string }) | null
  if (!response.ok) {
    throw new Error(data?.detail ?? fallback)
  }
  if (data === null) {
    throw new Error(fallback)
  }
  return data
}

function beginPasskeyLogin() {
  return postPasskeyPublic<PasskeyBeginResponse>(
    '/api/app/passkeys/login/begin',
    {},
    'Failed to start passkey sign-in',
  )
}

/**
 * Adopts a token minted by the passkey endpoint. pb.authStore.save is what
 * authWithPassword does internally, so the gate, the meCache invalidation and
 * every later request behave identically no matter how the session was created.
 */
async function finishPasskeyLogin(sessionId: string, credential: Credential) {
  const data = await postPasskeyPublic<PasskeyAuthResponse>(
    '/api/app/passkeys/login/finish',
    { session_id: sessionId, credential: assertionToJSON(credential) },
    'Passkey sign-in failed',
  )
  clearMeCache()
  pb.authStore.save(data.token, data.record)
}

/**
 * Signs in with a discoverable passkey. Usernameless: the authenticator picks the
 * account, so nothing is typed and no email is sent to the server first.
 */
export async function loginWithPasskey() {
  if (!passkeysSupported()) {
    throw new Error(passkeyUnavailableHint())
  }
  const begin = await beginPasskeyLogin()

  let credential: Credential | null
  try {
    credential = await navigator.credentials.get({ publicKey: toRequestOptions(begin.options) })
  } catch (err) {
    // An abort is ours, so it is rethrown for the caller to swallow rather than
    // being turned into a message.
    if (isAbortError(err)) {
      throw err
    }
    throw new Error(passkeyErrorMessage(err, 'login'), { cause: err })
  }
  if (!credential) {
    throw new Error('No passkey was selected.')
  }
  await finishPasskeyLogin(begin.session_id, credential)
}

export type ConditionalPasskeyLogin = {
  /**
   * Cancels the outstanding request and resolves once the browser has released
   * it. Must be awaited before any other credentials.get() call.
   */
  cancel: () => Promise<void>
}

/**
 * Arms the browser's autofill so a passkey can be offered from the email field.
 *
 * Returns null — silently — when the browser cannot do this or the server will
 * not issue a challenge. A background offer the user never asked for must never
 * put an error on the login screen, so onError fires only for a failure *after*
 * they picked a credential.
 */
export async function startConditionalPasskeyLogin(handlers: {
  onSuccess: () => void
  onError: (message: string) => void
}): Promise<ConditionalPasskeyLogin | null> {
  if (!(await conditionalMediationAvailable())) {
    return null
  }
  let begin: PasskeyBeginResponse
  try {
    begin = await beginPasskeyLogin()
  } catch {
    return null
  }

  const controller = new AbortController()
  const settled = (async () => {
    try {
      const credential = await navigator.credentials.get({
        publicKey: toRequestOptions(begin.options),
        mediation: 'conditional',
        signal: controller.signal,
      })
      if (!credential) {
        return
      }
      await finishPasskeyLogin(begin.session_id, credential)
      handlers.onSuccess()
    } catch (err) {
      if (isAbortError(err)) {
        return
      }
      handlers.onError(passkeyErrorMessage(err, 'login'))
    }
  })()

  return {
    async cancel() {
      controller.abort()
      // Awaiting is the load-bearing part: a second credentials.get() rejects
      // while one is still outstanding, and abort() alone does not guarantee the
      // browser has let go by the time the next call starts. The promise never
      // rejects — every path above is caught.
      await settled
    },
  }
}

export type MeInfo = {
  email: string
  is_admin: boolean
}

let meCache: MeInfo | null = null

export function clearMeCache() {
  meCache = null
}

pb.authStore.onChange(() => {
  meCache = null
})

export async function getMe(): Promise<MeInfo> {
  await ensureAuth()
  if (meCache) {
    return meCache
  }

  const response = await fetch(`${pbUrl}/api/app/me`, {
    headers: { Authorization: pb.authStore.token },
  })
  const data = (await response.json()) as MeInfo & { detail?: string }
  if (!response.ok) {
    throw new Error(data.detail ?? 'Failed to load account info')
  }
  meCache = {
    email: typeof data.email === 'string' ? data.email : '',
    is_admin: Boolean(data.is_admin),
  }
  return meCache
}

/** True when the current users session is a paired admin (or rare superuser JWT). */
export async function isAdmin() {
  if (!pb.authStore.isValid) {
    return false
  }
  try {
    return (await getMe()).is_admin
  } catch {
    return false
  }
}

export function logout() {
  clearMeCache()
  pb.authStore.clear()
}

export function getUserDisplayName() {
  const record = pb.authStore.record
  if (!record) {
    return ''
  }

  const name = typeof record.name === 'string' ? record.name.trim() : ''
  const email = typeof record.email === 'string' ? record.email.trim() : ''
  return name || email
}
