import { test, expect } from '@playwright/test'
import { credentials, loginAsUser, logout } from './helpers/auth'

test('login succeeds for regular user', async ({ page }) => {
  await loginAsUser(page)
  await expect(page.getByRole('link', { name: 'Documents' })).toBeVisible()
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
