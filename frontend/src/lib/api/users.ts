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
