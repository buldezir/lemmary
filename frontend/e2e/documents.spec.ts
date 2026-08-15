import { test, expect, type Page } from '@playwright/test'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { loginAsSuper, loginAsUser, openMoreMenu, uploadFixture, waitForProcessing } from './helpers/auth'

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
  await expect(page.getByRole('menuitem', { name: 'Admin' })).toBeVisible()
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

test('delete document from detail page', async ({ page }) => {
  await loginAsUser(page)
  await uploadFixture(page, 'sample.txt')

  const documentURL = page.url()
  const documentId = documentURL.match(/\/document\/([^/?#]+)/)?.[1]
  expect(documentId).toBeTruthy()

  page.once('dialog', (dialog) => dialog.accept())
  await page.getByRole('button', { name: 'Delete' }).click()

  await expect(page).toHaveURL(/\/$/)
  await expect(page.getByRole('heading', { name: 'Documents' })).toBeVisible()
  await expect(page.getByText('Loading documents...')).toHaveCount(0)
  await expect(page.locator(`main a[href="/document/${documentId}"]`)).toHaveCount(0)
})

test('cancel document delete keeps detail page', async ({ page }) => {
  await loginAsUser(page)
  await uploadFixture(page, 'sample.txt')

  const documentURL = page.url()
  const documentId = documentURL.match(/\/document\/([^/?#]+)/)?.[1]
  expect(documentId).toBeTruthy()

  page.once('dialog', (dialog) => dialog.dismiss())
  await page.getByRole('button', { name: 'Delete' }).click()

  await expect(page).toHaveURL(new RegExp(`/document/${documentId}`))
  await expect(page.getByRole('button', { name: 'Delete' })).toBeEnabled()
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

test('standard search matches document tags', async ({ page }) => {
  const stamp = `${Date.now()}`
  const title = `Tag Search Doc ${stamp}`
  const tag = `taghit${stamp}`

  await loginAsUser(page)
  await uploadFixture(page, 'sample.txt')
  await waitForProcessing(page)
  const documentId = documentIdFromURL(page)
  await saveDocumentMetadata(page, { title, tags: tag })

  await goToDocuments(page)
  const searchBox = page.getByPlaceholder('Search title, tags, purpose, summary...')
  await searchBox.fill(`nomatch${stamp}`)
  await expect(documentLink(page, documentId)).toHaveCount(0)

  await searchBox.fill(tag)
  await expect(documentLink(page, documentId)).toBeVisible()
  await expect(page.getByRole('heading', { name: title })).toBeVisible()
})

test('document type and correspondent filters are typeahead dropdowns', async ({ page }) => {
  const stamp = `${Date.now()}`
  const typeA = `TypeAlpha ${stamp}`
  const typeB = `TypeBeta ${stamp}`
  const corrA = `CorrAlpha ${stamp}`
  const corrB = `CorrBeta ${stamp}`
  const titleA = `Typeahead A ${stamp}`
  const titleB = `Typeahead B ${stamp}`

  await loginAsUser(page)
  await uploadFixture(page, 'sample.txt')
  await waitForProcessing(page)
  const documentA = documentIdFromURL(page)
  await saveDocumentMetadata(page, { title: titleA, documentType: typeA, correspondent: corrA })

  await uploadFixture(page, 'sample.txt')
  await waitForProcessing(page)
  const documentB = documentIdFromURL(page)
  await saveDocumentMetadata(page, { title: titleB, documentType: typeB, correspondent: corrB })

  await goToDocuments(page)

  const typeInput = page.getByLabel('Document type')
  await typeInput.fill(typeA)
  await page.getByRole('option', { name: typeA, exact: true }).click()
  await expect(documentLink(page, documentA)).toBeVisible()
  await expect(documentLink(page, documentB)).toHaveCount(0)

  await typeInput.click()
  await page.getByRole('option', { name: 'All types', exact: true }).click()
  await expect(documentLink(page, documentA)).toBeVisible()
  await expect(documentLink(page, documentB)).toBeVisible()

  const corrInput = page.getByLabel('Correspondent')
  await corrInput.fill(corrB)
  await page.getByRole('option', { name: corrB, exact: true }).click()
  await expect(documentLink(page, documentB)).toBeVisible()
  await expect(documentLink(page, documentA)).toHaveCount(0)
})

async function saveDocumentMetadata(
  page: Page,
  fields: {
    title?: string
    documentType?: string
    correspondent?: string
    tags?: string
  },
) {
  await page.getByRole('button', { name: 'Unblock editing' }).click()
  if (fields.title !== undefined) {
    await page.getByLabel('Title', { exact: true }).fill(fields.title)
  }
  if (fields.documentType !== undefined) {
    await page.getByLabel('Document type').fill(fields.documentType)
  }
  if (fields.correspondent !== undefined) {
    await page.getByLabel('Correspondent').fill(fields.correspondent)
  }
  if (fields.tags !== undefined) {
    await page.getByLabel('Tags (comma separated)').fill(fields.tags)
  }
  await page.getByRole('button', { name: 'Save corrections' }).click()
  await expect(page.getByText('Metadata saved.')).toBeVisible()
}

async function goToDocuments(page: Page) {
  await page.getByRole('link', { name: 'Documents', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Documents' })).toBeVisible()
  await expect(page.getByText('Loading documents...')).toHaveCount(0)
}

function documentIdFromURL(page: Page) {
  const documentId = page.url().match(/\/document\/([^/?#]+)/)?.[1]
  expect(documentId).toBeTruthy()
  return documentId as string
}

function documentLink(page: Page, documentId: string) {
  return page.locator(`main a[href="/document/${documentId}"]`)
}
