import { test, expect, type Page } from '@playwright/test'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import {
  loginAsSuper,
  loginAsUser,
  openMoreMenu,
  uniqueFixturePayload,
  uploadFixture,
  waitForProcessing,
} from './helpers/auth'

const fixturesDir = path.join(path.dirname(fileURLToPath(import.meta.url)), 'fixtures')

test('upload txt document and see it on list', async ({ page }) => {
  await loginAsUser(page)
  await uploadFixture(page, 'sample.txt')
  await expect(page).toHaveURL(/\/document\//)

  await page.getByRole('link', { name: 'Documents', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Documents' })).toBeVisible()
  await expect(page.locator('main a[href*="/document/"]').first()).toBeVisible()
})

test('upload multiple files and return to documents list', async ({ page }) => {
  await loginAsUser(page)
  await page.getByRole('link', { name: 'Upload', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Upload documents' })).toBeVisible()

  const first = uniqueFixturePayload('sample.txt', 'batch-a.txt')
  const second = uniqueFixturePayload('sample.csv', 'batch-b.csv')
  await page.locator('input[type="file"]').setInputFiles([first, second])
  await expect(page.getByText('batch-a.txt')).toBeVisible()
  await expect(page.getByText('batch-b.csv')).toBeVisible()

  await page.getByRole('button', { name: 'Upload and process' }).click()
  await expect(page).toHaveURL(/\/$/)
  await expect(page.getByRole('heading', { name: 'Documents' })).toBeVisible()
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
  await expect(page.locator(`main [data-document-id="${documentId}"]`)).toHaveCount(0)
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
  await expect(page.getByRole('heading', { name: 'Upload documents' })).toBeVisible()
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

test('standard search matches OCR text', async ({ page }) => {
  const stamp = `${Date.now()}`
  const title = `OCR Search Doc ${stamp}`

  await loginAsUser(page)
  await uploadFixture(page, 'sample.txt')
  await waitForProcessing(page)
  const documentId = documentIdFromURL(page)
  await saveDocumentMetadata(page, { title })

  await goToDocuments(page)
  const searchBox = page.getByPlaceholder('Search title, tags, purpose, summary...')
  await searchBox.fill(`nomatch${stamp}`)
  await expect(documentLink(page, documentId)).toHaveCount(0)

  await searchBox.fill('Acme Plumbing')
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

test('needs-review document card shows duplicate reason instead of empty summary', async ({
  page,
}) => {
  const stamp = `${Date.now()}`
  const originalTitle = `Original Invoice ${stamp}`
  const duplicateTitle = `Possible Dup ${stamp}`

  await loginAsUser(page)
  await uploadFixture(page, 'sample.txt')
  await waitForProcessing(page)
  const originalId = documentIdFromURL(page)
  await saveDocumentMetadata(page, { title: originalTitle })

  await uploadFixture(page, 'sample.txt')
  await waitForProcessing(page)
  const duplicateId = documentIdFromURL(page)
  await saveDocumentMetadata(page, { title: duplicateTitle })
  await patchDocument(page, duplicateId, {
    processing_status: 'needs_review',
    summary: '',
    purpose: '',
    duplicate_of: originalId,
  })

  await goToDocuments(page)
  const card = documentLink(page, duplicateId)
  await expect(card).toContainText('Needs review')
  await expect(card).toContainText(`Possible duplicate of ${originalTitle}.`)
  await expect(card).not.toContainText('No summary yet.')

  await card.getByRole('link', { name: originalTitle, exact: true }).click()
  await expect(page).toHaveURL(new RegExp(`/document/${originalId}`))
  await expect(page.getByRole('heading', { name: originalTitle })).toBeVisible()
})

test('needs-review document card shows low confidence instead of empty summary', async ({
  page,
}) => {
  const stamp = `${Date.now()}`
  const title = `Low Confidence ${stamp}`

  await loginAsUser(page)
  await uploadFixture(page, 'sample.txt')
  await waitForProcessing(page)
  const documentId = documentIdFromURL(page)
  await saveDocumentMetadata(page, { title })
  await patchDocument(page, documentId, {
    processing_status: 'needs_review',
    summary: '',
    purpose: '',
    confidence: 0.32,
    duplicate_of: '',
  })

  await goToDocuments(page)
  const card = documentLink(page, documentId)
  await expect(card).toContainText('Needs review')
  await expect(card).toContainText('Low extraction confidence (32%).')
  await expect(card).not.toContainText('No summary yet.')
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

async function patchDocument(page: Page, documentId: string, data: Record<string, unknown>) {
  const error = await page.evaluate(
    async ({ documentId, data }) => {
      const raw = localStorage.getItem('pocketbase_auth')
      if (!raw) {
        return 'missing pocketbase_auth'
      }
      const parsed = JSON.parse(raw) as { token?: string }
      if (!parsed.token) {
        return 'missing auth token'
      }
      const response = await fetch(`/api/collections/documents/records/${documentId}`, {
        method: 'PATCH',
        headers: {
          Authorization: parsed.token,
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(data),
      })
      if (!response.ok) {
        return await response.text()
      }
      return ''
    },
    { documentId, data },
  )
  expect(error, `failed to patch document ${documentId}`).toBe('')
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
  return page.locator(`main [data-document-id="${documentId}"]`)
}
