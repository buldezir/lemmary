import { type Page, expect } from '@playwright/test'

export const credentials = {
  user: {
    email: process.env.E2E_USER_EMAIL ?? 'e2e@paperless.local',
    password: process.env.E2E_USER_PASSWORD ?? 'e2epassword123',
  },
  super: {
    email: process.env.E2E_SUPER_EMAIL ?? 'admin@paperless.local',
    password: process.env.E2E_SUPER_PASSWORD ?? 'adminpassword123',
  },
}

export async function login(page: Page, email: string, password: string) {
  await page.goto('/')
  await expect(page.getByRole('heading', { name: 'Paperless Go' })).toBeVisible()
  await page.getByLabel('Email').fill(email)
  await page.getByLabel('Password').fill(password)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByRole('link', { name: 'Documents', exact: true })).toBeVisible()
}

export async function loginAsUser(page: Page) {
  await login(page, credentials.user.email, credentials.user.password)
}

export async function loginAsSuper(page: Page) {
  await login(page, credentials.super.email, credentials.super.password)
}

export async function logout(page: Page) {
  await page.getByRole('button', { name: 'Log out' }).click()
  await expect(page.getByRole('heading', { name: 'Paperless Go' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Sign in' })).toBeVisible()
}

export async function uploadFixture(page: Page, fixtureName: string) {
  await page.getByRole('link', { name: 'Upload', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Upload document' })).toBeVisible()
  await page.locator('input[type="file"]').setInputFiles(`e2e/fixtures/${fixtureName}`)
  await page.getByRole('button', { name: 'Upload and process' }).click()
  await expect(page).toHaveURL(/\/document\//)
}

export async function waitForProcessing(page: Page) {
  await expect
    .poll(
      async () => {
        const status = await page.getByText(/Status:/i).innerText()
        if (/completed|needs_review/i.test(status)) return 'done'
        if (/failed/i.test(status)) return 'failed'
        return 'pending'
      },
      { timeout: 90_000, intervals: [500, 1000, 2000] },
    )
    .toBe('done')
}
