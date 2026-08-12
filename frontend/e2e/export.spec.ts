import { test, expect } from '@playwright/test'
import { loginAsUser, openMoreMenu } from './helpers/auth'

test('regular user can open export page and see modes', async ({ page }) => {
  await loginAsUser(page)
  await openMoreMenu(page)
  await page.getByRole('menuitem', { name: 'Export' }).click()
  await expect(page.getByRole('heading', { name: 'Export archive' })).toBeVisible()
  await expect(page.getByRole('radio', { name: /Original files only/i })).toBeChecked()
  await expect(page.getByRole('radio', { name: /Originals with OCR text/i })).toBeVisible()
  await expect(page.getByRole('radio', { name: /Originals with OCR and metadata/i })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Download archive' })).toBeVisible()
})

test('download archive starts without error', async ({ page }) => {
  await loginAsUser(page)
  await page.goto('/export')
  await expect(page.getByRole('heading', { name: 'Export archive' })).toBeVisible()

  const downloadPromise = page.waitForEvent('download', { timeout: 30_000 })
  await page.getByRole('button', { name: 'Download archive' }).click()
  const download = await downloadPromise
  expect(download.suggestedFilename()).toBe('paperless-export.zip')
  await expect(page.getByText('Archive download started.')).toBeVisible()
})
