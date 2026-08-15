import { test, expect } from '@playwright/test'
import { loginAsSuper, loginAsUser, openMoreMenu, openSettings } from './helpers/auth'

test('regular user cannot open settings', async ({ page }) => {
  await loginAsUser(page)
  await openMoreMenu(page)
  await expect(page.getByRole('menuitem', { name: 'Settings' })).toHaveCount(0)
  await expect(page.getByRole('menuitem', { name: 'Admin' })).toHaveCount(0)
  await page.goto('/settings')
  await expect(page.getByRole('heading', { name: 'Settings' })).toHaveCount(0)
  await expect(page.getByRole('heading', { name: 'Documents' })).toBeVisible()
})

test('superuser can load and save settings', async ({ page }) => {
  await loginAsSuper(page)
  await openSettings(page)

  await expect(page.getByRole('heading', { name: 'Providers' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Models' })).toBeVisible()
  const extractionModel = page.getByLabel('Extraction model')
  await expect(extractionModel.locator('option[value="e2e-mock"]')).toHaveCount(1, { timeout: 15_000 })
  await expect(page.getByLabel('Extraction provider').locator('option', { hasText: 'Mistral' })).toHaveCount(1)
  await extractionModel.selectOption('e2e-mock-updated')
  await page.getByRole('button', { name: 'Save settings' }).click()
  await expect(page.getByText('Settings saved. Runtime reloaded.')).toBeVisible({ timeout: 15_000 })
})

test('superuser can scan for duplicates', async ({ page }) => {
  await loginAsSuper(page)
  await openSettings(page)
  await page.getByRole('button', { name: 'Scan for duplicates' }).click()
  await expect(page.getByText(/Scan finished:/i)).toBeVisible({ timeout: 30_000 })
})

test('superuser can rebuild search index', async ({ page }) => {
  await loginAsSuper(page)
  await openSettings(page)
  await page.getByRole('button', { name: 'Rebuild search index' }).click()
  await expect(page.getByText(/Reindexed \d+ documents\./i)).toBeVisible({ timeout: 30_000 })
})
