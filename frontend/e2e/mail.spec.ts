import { test, expect } from '@playwright/test'
import { credentials, loginAsUser } from './helpers/auth'

async function apiAuth(request: import('@playwright/test').APIRequestContext) {
  const res = await request.post('/api/collections/users/auth-with-password', {
    data: {
      identity: credentials.user.email,
      password: credentials.user.password,
    },
  })
  expect(res.ok()).toBeTruthy()
  const body = await res.json()
  return body.token as string
}

test('mail page shows connect and scan UI', async ({ page, request }) => {
  const token = await apiAuth(request)

  await loginAsUser(page)
  await page.getByRole('link', { name: 'Mail', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Mail import' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Connect Gmail' })).toBeVisible()

  const create = await request.post('/api/app/mail/accounts', {
    headers: { Authorization: token },
    data: { email: credentials.user.email, refresh_token: 'e2e-browser-token' },
  })
  expect(create.ok()).toBeTruthy()
  const account = await create.json()

  await page.reload()
  await expect(page.getByText(credentials.user.email)).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Manual scan', level: 3 })).toBeVisible()
  await expect(page.getByLabel('From date')).toBeVisible()
  await expect(page.getByLabel('To date')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Start scan' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Scan history' })).toBeVisible()

  await page.getByLabel('From date').fill('2024-07-01')
  await page.getByLabel('To date').fill('2024-06-01')
  await page.getByRole('button', { name: 'Start scan' }).click()
  await expect(page.getByRole('alert')).toBeVisible({ timeout: 10_000 })

  await request.delete(`/api/app/mail/accounts/${account.id}`, {
    headers: { Authorization: token },
  })
})
