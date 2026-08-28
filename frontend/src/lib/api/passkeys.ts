import { apiFetch } from '../apiClient'
import {
  passkeyErrorMessage,
  passkeysSupported,
  passkeyUnavailableHint,
  registrationToJSON,
  toCreationOptions,
} from '../webauthn'

export type Passkey = {
  id: string
  name: string
  created: string
  last_used: string
}

type PasskeyListResponse = { passkeys?: Passkey[] }
type PasskeyResponse = { passkey: Passkey }
type PasskeyBeginResponse = { session_id: string; options: unknown }

export async function listPasskeys(): Promise<Passkey[]> {
  const data = await apiFetch<PasskeyListResponse>('/api/app/passkeys', {
    fallbackError: 'Failed to load passkeys',
  })
  return data.passkeys ?? []
}

export function renamePasskey(id: string, name: string) {
  return apiFetch<PasskeyResponse>(`/api/app/passkeys/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    body: { name },
    fallbackError: 'Failed to rename the passkey',
  })
}

export function deletePasskey(id: string) {
  return apiFetch<unknown>(`/api/app/passkeys/${encodeURIComponent(id)}`, {
    method: 'DELETE',
    fallbackError: 'Failed to remove the passkey',
  })
}

/**
 * Runs the registration ceremony: ask the server for options, hand them to the
 * authenticator, send the result back.
 *
 * Lives here rather than in auth.ts because it never touches the session — the
 * caller is already signed in, and apiFetch supplies the token.
 */
export async function registerPasskey(name: string): Promise<Passkey> {
  if (!passkeysSupported()) {
    throw new Error(passkeyUnavailableHint())
  }

  const begin = await apiFetch<PasskeyBeginResponse>('/api/app/passkeys/register/begin', {
    method: 'POST',
    body: {},
    fallbackError: 'Failed to start passkey setup',
  })

  let credential: Credential | null
  try {
    credential = await navigator.credentials.create({ publicKey: toCreationOptions(begin.options) })
  } catch (err) {
    throw new Error(passkeyErrorMessage(err, 'register'), { cause: err })
  }
  if (!credential) {
    throw new Error('No passkey was created.')
  }

  const data = await apiFetch<PasskeyResponse>('/api/app/passkeys/register/finish', {
    method: 'POST',
    body: {
      session_id: begin.session_id,
      name,
      credential: registrationToJSON(credential),
    },
    fallbackError: 'Failed to save the passkey',
  })
  return data.passkey
}

/**
 * Renders a PocketBase timestamp as a plain date, matching how DocumentCard
 * formats one. Deliberately not toLocaleDateString: that would make the browser
 * e2e assertions depend on the runner's locale.
 */
export function passkeyDateLabel(value: string): string {
  const trimmed = value.trim()
  if (!trimmed) {
    return 'Never'
  }
  return trimmed.slice(0, 10)
}
