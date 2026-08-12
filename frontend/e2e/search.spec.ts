import { test, expect } from '@playwright/test'
import { loginAsUser, uploadFixture, waitForProcessing } from './helpers/auth'

test('deep search returns document hits', async ({ page }) => {
  await loginAsUser(page)
  await uploadFixture(page, 'sample.png')
  await waitForProcessing(page)

  await page.getByRole('link', { name: 'Deep Search' }).click()
  await expect(page.getByRole('heading', { name: 'Deep Search' })).toBeVisible()

  await page.getByPlaceholder('Describe what you are looking for...').fill('Find the Acme Plumbing invoice')
  await page.getByRole('button', { name: 'Search' }).click()

  await expect(page.getByText(/Acme Plumbing|invoice|leak/i).first()).toBeVisible({
    timeout: 30_000,
  })
  await expect(page.locator('a[href*="/document/"]').first()).toBeVisible({ timeout: 30_000 })
})

test('deep mode toggle is available', async ({ page }) => {
  await loginAsUser(page)
  await page.getByRole('link', { name: 'Deep Search' }).click()
  await expect(page.getByText(/Deep mode/i)).toBeVisible()
  await page.getByRole('checkbox', { name: /Deep mode/i }).check()
  await expect(page.getByRole('checkbox', { name: /Deep mode/i })).toBeChecked()
})
