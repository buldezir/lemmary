import { test, expect } from '@playwright/test'
import { loginAsSuper, loginAsUser, openMoreMenu } from './helpers/auth'

test('regular user cannot open import', async ({ page }) => {
  await loginAsUser(page)
  await openMoreMenu(page)
  await expect(page.getByRole('menuitem', { name: 'Import' })).toHaveCount(0)
  await page.goto('/import')
  await expect(page.getByRole('heading', { name: 'Import from Paperless-ngx' })).toHaveCount(0)
  await expect(page.getByRole('heading', { name: 'Documents' })).toBeVisible()
})

test('superuser can open import page', async ({ page }) => {
  await loginAsSuper(page)
  await openMoreMenu(page)
  await page.getByRole('menuitem', { name: 'Import' }).click()
  await expect(page.getByRole('heading', { name: 'Import from Paperless-ngx' })).toBeVisible()
  await expect(page.getByLabel('Paperless-ngx URL')).toBeVisible()
  await expect(page.getByLabel('API key')).toBeVisible()
  await expect(page.getByRole('radio', { name: /Keep Paperless-ngx metadata/i })).toBeChecked()
  await expect(page.getByRole('radio', { name: /Import files only and reprocess/i })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Start import' })).toBeVisible()
})
