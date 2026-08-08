import { test, expect } from '@playwright/test'
import { loginAsUser, uploadFixture, waitForProcessing } from './helpers/auth'

test('pipeline reaches completed with OCR text', async ({ page }) => {
  await loginAsUser(page)
  await uploadFixture(page, 'sample.png')
  await waitForProcessing(page)
  await expect(page.getByLabel('OCR text')).toContainText(/Acme Plumbing/i)
})

test('csv upload uses native text extraction', async ({ page }) => {
  await loginAsUser(page)
  await uploadFixture(page, 'sample.csv')
  await waitForProcessing(page)
  await expect(page.getByLabel('OCR text')).toContainText(/Paper/i)
})
