import { apiFetch } from '../apiClient'

export type UserSummary = {
  id: string
  email: string
  name: string
}

/** Every account, oldest first. Admin only: the users collection shows a session only itself. */
export function listUsers() {
  return apiFetch<UserSummary[]>('/api/app/users', {
    fallbackError: 'Failed to load users',
  })
}

/** An account as the admin's Users tab sees it; admins are listed but not editable. */
export type ManagedUser = UserSummary & {
  admin: boolean
  created: string
}

export type ManagedUserInput = {
  email?: string
  name?: string
  password?: string
}

export function listManagedUsers() {
  return apiFetch<ManagedUser[]>('/api/app/admin/users', {
    fallbackError: 'Failed to load users',
  })
}

export function createManagedUser(input: ManagedUserInput) {
  return apiFetch<ManagedUser>('/api/app/admin/users', {
    method: 'POST',
    body: input,
    fallbackError: 'Failed to create the user',
  })
}

export function updateManagedUser(id: string, input: ManagedUserInput) {
  return apiFetch<ManagedUser>(`/api/app/admin/users/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    body: input,
    fallbackError: 'Failed to update the user',
  })
}

export function deleteManagedUser(id: string) {
  return apiFetch<void>(`/api/app/admin/users/${encodeURIComponent(id)}`, {
    method: 'DELETE',
    fallbackError: 'Failed to delete the user',
  })
}
