import { test, expect } from '@playwright/test'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { loginAsSuper, loginAsUser, openMoreMenu, uploadFixture } from './helpers/auth'

const fixturesDir = path.join(path.dirname(fileURLToPath(import.meta.url)), 'fixtures')

test('upload txt document and see it on list', async ({ page }) => {
  await loginAsUser(page)
  await uploadFixture(page, 'sample.txt')
  await expect(page).toHaveURL(/\/document\//)

  await page.getByRole('link', { name: 'Documents', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Documents' })).toBeVisible()
  await expect(page.locator('main a[href*="/document/"]').first()).toBeVisible()
})

test('admin can upload documents', async ({ page }) => {
  await loginAsSuper(page)
  await openMoreMenu(page)
  await expect(page.getByRole('menuitem', { name: 'Settings' })).toBeVisible()
  await uploadFixture(page, 'sample.txt')
  await expect(page).toHaveURL(/\/document\//)
})

test('edit document title on detail page', async ({ page }) => {
  await loginAsUser(page)
  await uploadFixture(page, 'sample.txt')

  await page.getByRole('button', { name: 'Unblock editing' }).click()
  await page.getByLabel('Title', { exact: true }).fill('E2E Edited Title')
  await page.getByRole('button', { name: 'Save corrections' }).click()
  await expect(page.getByRole('heading', { name: 'E2E Edited Title' })).toBeVisible()
})

test('reject duplicate file upload', async ({ page }) => {
  await loginAsUser(page)

  const fixturePath = path.join(fixturesDir, 'sample.txt')
  const exactBytes = fs.readFileSync(fixturePath)
  const payload = {
    name: 'sample.txt',
    mimeType: 'text/plain',
    buffer: exactBytes,
  }

  await page.getByRole('link', { name: 'Upload', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Upload document' })).toBeVisible()
  await page.locator('input[type="file"]').setInputFiles(payload)
  await page.getByRole('button', { name: 'Upload and process' }).click()
  await expect(page).toHaveURL(/\/document\//)
  const firstURL = page.url()

  await page.getByRole('link', { name: 'Upload', exact: true }).click()
  await page.locator('input[type="file"]').setInputFiles(payload)
  await page.getByRole('button', { name: 'Upload and process' }).click()
  await expect(page.getByText(/duplicate/i)).toBeVisible({ timeout: 15_000 })
  await expect(page.getByRole('link', { name: 'Open existing document' })).toBeVisible()
  await expect(page).not.toHaveURL(firstURL)
})
