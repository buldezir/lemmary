import { test, expect } from '@playwright/test'
import { loginAsUser } from './helpers/auth'

test('upload sub-sections are reachable and files upload is the default', async ({ page }) => {
  await loginAsUser(page)
  await page.getByRole('link', { name: 'Upload', exact: true }).click()

  const sources = page.getByRole('navigation', { name: 'Upload sources' })
  await expect(page).toHaveURL(/\/upload$/)
  await expect(page.getByRole('heading', { name: 'Upload documents' })).toBeVisible()

  await sources.getByRole('link', { name: 'Amazon orders' }).click()
  await expect(page).toHaveURL(/\/upload\/amazon$/)
  await expect(page.getByRole('heading', { name: 'Import Amazon orders' })).toBeVisible()
  await expect(page.locator('input[type="file"]')).toHaveCount(0)

  await sources.getByRole('link', { name: 'Split documents' }).click()
  await expect(page).toHaveURL(/\/upload\/split$/)
  await expect(page.getByRole('heading', { name: 'Split documents' })).toBeVisible()

  await sources.getByRole('link', { name: 'Files' }).click()
  await expect(page).toHaveURL(/\/upload$/)
  await expect(page.getByRole('button', { name: 'Upload and process' })).toBeVisible()
})

test('upload sub-sections can be opened directly by URL', async ({ page }) => {
  await loginAsUser(page)

  await page.goto('/upload/amazon')
  await expect(page.getByRole('heading', { name: 'Import Amazon orders' })).toBeVisible()

  await page.goto('/upload/split')
  await expect(page.getByRole('heading', { name: 'Split documents' })).toBeVisible()
})
