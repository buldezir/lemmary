import { test, expect } from '@playwright/test'
import { loginAsSuper, loginAsUser, openManagement, openMoreMenu, openSettings } from './helpers/auth'

test('regular user cannot open settings', async ({ page }) => {
  await loginAsUser(page)
  await openMoreMenu(page)
  await expect(page.getByRole('menuitem', { name: 'Settings' })).toHaveCount(0)
  await expect(page.getByRole('menuitem', { name: 'Management' })).toHaveCount(0)
  await expect(page.getByRole('menuitem', { name: 'Admin' })).toHaveCount(0)
  await page.goto('/settings')
  await expect(page.getByRole('heading', { name: 'Settings' })).toHaveCount(0)
  await expect(page.getByRole('heading', { name: 'Documents' })).toBeVisible()
  await page.goto('/management')
  await expect(page.getByRole('heading', { name: 'Management' })).toHaveCount(0)
  await expect(page.getByRole('heading', { name: 'Documents' })).toBeVisible()
})

test('superuser can load and save settings', async ({ page }) => {
  await loginAsSuper(page)
  await openSettings(page)

  await expect(page.getByRole('heading', { name: 'Providers' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Models' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Save settings' })).toHaveCount(2)
  await expect(page.getByRole('button', { name: 'Save settings' }).first()).toHaveCSS('cursor', 'pointer')
  const extractionModel = page.getByLabel('Extraction model')
  await expect(extractionModel.locator('option[value="e2e-mock"]')).toHaveCount(1, { timeout: 15_000 })
  await expect(page.getByLabel('Extraction provider').locator('option', { hasText: 'Mistral' })).toHaveCount(1)
  await extractionModel.selectOption('e2e-mock-updated')
  await page.getByRole('button', { name: 'Save settings' }).first().click()
  await expect(page.getByText('Settings saved. Runtime reloaded.')).toBeVisible({ timeout: 15_000 })
})

test('each model option explains what it is used for', async ({ page }) => {
  await loginAsSuper(page)
  await openSettings(page)

  await expect(
    page.getByText('Reads the text out of uploaded PDFs, images and scans.'),
  ).toBeVisible()
  await expect(
    page.getByText("Turns a document's text into its title, date, type, correspondent"),
  ).toBeVisible()
  await expect(page.getByText('Answers questions about a single document')).toBeVisible()
  await expect(page.getByText('Answers natural-language queries on the Deep Search page')).toBeVisible()
  await expect(
    page.getByText('How long one extraction, chat, search or split-detection request may take.'),
  ).toBeVisible()

  // The hints sit outside the labels, so the fields stay addressable by label.
  await expect(page.getByLabel('AI timeout (seconds)')).toBeVisible()
  await expect(page.getByLabel('OCR timeout (seconds)')).toBeVisible()

  // Bookkeeping only, so it is not offered here.
  await expect(page.getByLabel('Extraction prompt version')).toHaveCount(0)
})

test('superuser can scan for duplicates', async ({ page }) => {
  await loginAsSuper(page)
  await openManagement(page)
  await page.getByRole('button', { name: 'Scan for duplicates' }).click()
  await expect(page.getByText(/Scan finished:/i)).toBeVisible({ timeout: 30_000 })
})

test('superuser can rebuild search index', async ({ page }) => {
  await loginAsSuper(page)
  await openManagement(page)
  await page.getByRole('button', { name: 'Rebuild search index' }).click()
  await expect(page.getByText(/Reindexed \d+ documents\./i)).toBeVisible({ timeout: 30_000 })
})

test('superuser can clear stale data once the queue is idle', async ({ page }) => {
  await loginAsSuper(page)
  await openManagement(page)
  const clear = page.getByRole('button', { name: 'Clear stale data' })
  // The button is gated on in-flight processing jobs left by earlier specs.
  await expect(clear).toBeEnabled({ timeout: 60_000 })
  await expect(clear).toHaveCSS('cursor', 'pointer')
  await clear.click()
  await expect(page.getByText(/Removed \d+ tags?, \d+ correspondents?, \d+ document types?\./i)).toBeVisible({
    timeout: 30_000,
  })
})

test('clear stale data is blocked while documents are processing', async ({ page }) => {
  await loginAsSuper(page)
  await page.route('**/api/collections/processing_jobs/records?**', (route) =>
    route.fulfill({ json: { page: 1, perPage: 1, totalItems: 1, totalPages: 1, items: [] } }),
  )
  await openManagement(page)
  const clear = page.getByRole('button', { name: 'Clear stale data' })
  await expect(clear).toBeDisabled()
  await expect(clear).toHaveCSS('cursor', 'not-allowed')
  await expect(
    page.getByText('Waiting for the queue to drain: 1 job pending, 1 job running.'),
  ).toBeVisible()
  await expect(page.getByRole('button', { name: 'Rebuild search index' })).toBeEnabled()
})
