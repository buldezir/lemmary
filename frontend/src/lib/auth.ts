import { ClientResponseError } from 'pocketbase'
import { pb, pbUrl } from './pb'

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
