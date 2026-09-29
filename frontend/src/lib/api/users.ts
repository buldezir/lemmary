import { t } from '../../i18n'
import { apiFetch } from '../apiClient'

export type UserSummary = {
  id: string
  email: string
  name: string
}

/** Every account, oldest first. Admin only: the users collection shows a session only itself. */
export function listUsers() {
  return apiFetch<UserSummary[]>('/api/app/users', {
    fallbackError: t('users.loadFailed'),
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
    fallbackError: t('users.loadFailed'),
  })
}

export function createManagedUser(input: ManagedUserInput) {
  return apiFetch<ManagedUser>('/api/app/admin/users', {
    method: 'POST',
    body: input,
    fallbackError: t('users.createFailed'),
  })
}

export function updateManagedUser(id: string, input: ManagedUserInput) {
  return apiFetch<ManagedUser>(`/api/app/admin/users/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    body: input,
    fallbackError: t('users.updateFailed'),
  })
}

export function deleteManagedUser(id: string) {
  return apiFetch<void>(`/api/app/admin/users/${encodeURIComponent(id)}`, {
    method: 'DELETE',
    fallbackError: t('users.deleteFailed'),
  })
}
