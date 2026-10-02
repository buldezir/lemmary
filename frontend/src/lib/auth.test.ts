import { ClientResponseError } from 'pocketbase'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const signIn = vi.hoisted(() => vi.fn())
const refresh = vi.hoisted(() => vi.fn())
const save = vi.hoisted(() => vi.fn())
const clear = vi.hoisted(() => vi.fn())

vi.mock('./pb', () => ({
  pb: {
    collection: (name: string) => ({
      authWithPassword: (email: string, password: string) => signIn(name, email, password),
      authRefresh: () => refresh(name),
    }),
    authStore: { token: '', save, clear, onChange: () => {} },
  },
  pbUrl: '',
}))

import { adoptAuthHandoff, loginWithPassword, takeAuthHandoff } from './auth'

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

describe('takeAuthHandoff', () => {
  function at(hash: string) {
    const history = { state: { key: 'k' }, replaceState: vi.fn() }
    return { location: { hash, pathname: '/documents', search: '?tag=x' }, history }
  }

  it('takes the session out of the fragment and the address bar', () => {
    const { location, history } = at('#auth=eyJh.eyJi-c_d.sig')

    expect(takeAuthHandoff(location, history)).toBe('eyJh.eyJi-c_d.sig')
    expect(history.replaceState).toHaveBeenCalledWith({ key: 'k' }, '', '/documents?tag=x')
  })

  it('leaves any other fragment alone', () => {
    for (const hash of ['', '#section', '#auth=', '#auth=a%20b', '#auth=a&next=/']) {
      const { location, history } = at(hash)
      expect(takeAuthHandoff(location, history)).toBeNull()
      expect(history.replaceState).not.toHaveBeenCalled()
    }
  })
})

describe('adoptAuthHandoff', () => {
  beforeEach(() => {
    refresh.mockReset()
    save.mockReset()
    clear.mockReset()
  })

  it('signs a demo in with the handed-over session', async () => {
    refresh.mockResolvedValue({})

    await adoptAuthHandoff(true, 'token')

    expect(save).toHaveBeenCalledWith('token')
    expect(refresh).toHaveBeenCalledWith('users')
    expect(clear).not.toHaveBeenCalled()
  })

  it('drops a session the server does not accept', async () => {
    refresh.mockRejectedValue(rejection(401))

    await adoptAuthHandoff(true, 'stale')

    expect(clear).toHaveBeenCalled()
  })

  it('ignores a handover on anything but a demo', async () => {
    await adoptAuthHandoff(false, 'token')

    expect(save).not.toHaveBeenCalled()
    expect(refresh).not.toHaveBeenCalled()
  })
})
