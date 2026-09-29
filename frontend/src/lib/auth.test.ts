import { ClientResponseError } from 'pocketbase'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const signIn = vi.hoisted(() => vi.fn())

vi.mock('./pb', () => ({
  pb: {
    collection: (name: string) => ({
      authWithPassword: (email: string, password: string) => signIn(name, email, password),
    }),
    authStore: { token: '', clear: () => {}, onChange: () => {} },
  },
  pbUrl: '',
}))

import { loginWithPassword } from './auth'

function rejection(status: number) {
  return new ClientResponseError({ status, response: { message: `status ${status}` } })
}

describe('loginWithPassword', () => {
  beforeEach(() => {
    signIn.mockReset()
  })

  it('reports a wrong password, not the refused superuser fallback, on a managed instance', async () => {
    const wrongPassword = rejection(400)
    signIn.mockImplementation((collection: string) =>
      Promise.reject(collection === 'users' ? wrongPassword : rejection(403)),
    )

    await expect(loginWithPassword('admin@example.test', 'nope')).rejects.toBe(wrongPassword)
  })

  it('reports the superuser answer on an install that allows the fallback', async () => {
    const superuserAnswer = rejection(400)
    signIn.mockImplementation((collection: string) =>
      Promise.reject(collection === 'users' ? rejection(400) : superuserAnswer),
    )

    await expect(loginWithPassword('admin@example.test', 'nope')).rejects.toBe(superuserAnswer)
  })
})
