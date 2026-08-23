import { test, expect, type Page } from '@playwright/test'
import { credentials, loginAsUser, logout } from './helpers/auth'

test('login succeeds for regular user', async ({ page }) => {
  await loginAsUser(page)
  await expect(page.getByRole('link', { name: 'Documents', exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'More' }).click()
  await expect(page.getByRole('menuitem', { name: 'Settings' })).toHaveCount(0)
  await expect(page.getByRole('menuitem', { name: 'Admin' })).toHaveCount(0)
})

test('login rejects bad password', async ({ page }) => {
  await page.goto('/')
  await page.getByLabel('Email').fill(credentials.user.email)
  await page.getByLabel('Password').fill('wrong-password')
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByText(/failed|invalid|unable|wrong|something went wrong/i)).toBeVisible()
  await expect(page.getByRole('button', { name: 'Sign in' })).toBeVisible()
})

test('logout returns to login', async ({ page }) => {
  await loginAsUser(page)
  await logout(page)
})

// The real flow needs a configured provider and a third-party consent screen, so
// these stub the collection's auth-methods response — the one thing the login
// screen reads to decide which controls to render.
async function stubAuthMethods(
  page: Page,
  options: { password: boolean; providers: Array<{ name: string; displayName: string }> },
) {
  await page.route('**/api/collections/users/auth-methods*', (route) =>
    route.fulfill({
      json: {
        mfa: { enabled: false, duration: 0 },
        otp: { enabled: false, duration: 0 },
        password: { enabled: options.password, identityFields: ['email'] },
        oauth2: {
          enabled: options.providers.length > 0,
          providers: options.providers.map((provider) => ({
            ...provider,
            state: 'stub-state',
            authURL: 'https://example.test/authorize?state=stub-state&redirect_uri=',
            codeVerifier: '',
            codeChallenge: '',
            codeChallengeMethod: '',
          })),
        },
      },
    }),
  )
}

test('login offers enabled OAuth2 providers alongside the password form', async ({ page }) => {
  await stubAuthMethods(page, {
    password: true,
    providers: [
      { name: 'google', displayName: 'Google' },
      { name: 'oidc', displayName: 'Company SSO' },
    ],
  })
  await page.goto('/')

  await expect(page.getByRole('button', { name: 'Continue with Google' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Continue with Company SSO' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Sign in' })).toBeVisible()
})

test('login hides the password form when only OAuth2 is enabled', async ({ page }) => {
  await stubAuthMethods(page, {
    password: false,
    providers: [{ name: 'google', displayName: 'Google' }],
  })
  await page.goto('/')

  await expect(page.getByRole('button', { name: 'Continue with Google' })).toBeVisible()
  await expect(page.getByLabel('Password')).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Sign in' })).toHaveCount(0)
})

test('login keeps the password form when nothing is enabled', async ({ page }) => {
  await stubAuthMethods(page, { password: false, providers: [] })
  await page.goto('/')

  await expect(page.getByRole('button', { name: 'Sign in' })).toBeVisible()
  await expect(page.getByLabel('Password')).toBeVisible()
})
