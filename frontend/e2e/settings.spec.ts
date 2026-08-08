import { test, expect } from '@playwright/test'
import { loginAsSuper, loginAsUser } from './helpers/auth'

test('regular user cannot open settings', async ({ page }) => {
  await loginAsUser(page)
  await expect(page.getByRole('link', { name: 'Settings' })).toHaveCount(0)
  await page.goto('/settings')
  await expect(page.getByRole('heading', { name: 'Settings' })).toHaveCount(0)
  await expect(page.getByRole('heading', { name: 'Documents' })).toBeVisible()
})

test('superuser can load and save settings', async ({ page }) => {
  await loginAsSuper(page)
  await page.getByRole('link', { name: 'Settings' }).click()
  await expect(page.getByRole('heading', { name: 'Settings' })).toBeVisible()

  await page.getByLabel('Extraction model').fill('e2e-browser-model')
  await page.getByRole('button', { name: 'Save settings' }).click()
  await expect(page.getByText('Settings saved. Runtime reloaded.')).toBeVisible({ timeout: 15_000 })
})

test('superuser can scan for duplicates', async ({ page }) => {
  await loginAsSuper(page)
  await page.getByRole('link', { name: 'Settings' }).click()
  await expect(page.getByRole('heading', { name: 'Settings' })).toBeVisible()
  await page.getByRole('button', { name: 'Scan for duplicates' }).click()
  await expect(page.getByText(/Scan finished:/i)).toBeVisible({ timeout: 30_000 })
})
