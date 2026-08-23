import { test, expect } from '@playwright/test'
import { loginAsUser } from './helpers/auth'
import { multiPagePdf, uniqueScan } from './helpers/pdf'

function pdfUpload(name: string, buffer: Buffer) {
  return { name, mimeType: 'application/pdf', buffer }
}

test('a multi-document PDF is split into one document per part', async ({ page }) => {
  await loginAsUser(page)
  await page.goto('/upload/split')
  await expect(page.getByRole('heading', { name: 'Split documents' })).toBeVisible()

  await page
    .locator('input[type="file"]')
    .setInputFiles(pdfUpload('scan.pdf', uniqueScan(4, 'split')))

  await expect(page.getByText('4 pages. Click a gap between pages to cut there.')).toBeVisible()
  // Every page is rendered, so the user can see where a document starts.
  await expect(page.getByRole('button', { name: 'Enlarge page 4' })).toBeVisible()
  await expect(page.getByRole('img', { name: 'Page 1' })).toBeVisible()

  // Untouched, the whole file is one document.
  await expect(page.getByText('1 document: pages 1–4')).toBeVisible()

  await page.getByRole('button', { name: 'Split after page 2' }).click()
  await expect(page.getByText('2 documents: pages 1–2, 3–4')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Split after page 2' })).toHaveAttribute(
    'aria-pressed',
    'true',
  )

  // A second cut, then undoing it, proves the markers toggle.
  await page.getByRole('button', { name: 'Split after page 3' }).click()
  await expect(page.getByText('3 documents: pages 1–2, 3, 4')).toBeVisible()
  await page.getByRole('button', { name: 'Split after page 3' }).click()
  await expect(page.getByText('2 documents: pages 1–2, 3–4')).toBeVisible()

  await page.getByRole('button', { name: 'Split into 2 documents' }).click()
  await expect(page.getByText('Created 2 documents.')).toBeVisible()

  // The staged upload is consumed, so the page is back to its empty state.
  await expect(page.getByText('Choose a PDF to split')).toBeVisible()
  await page.getByRole('link', { name: 'Open documents' }).click()
  await expect(page).toHaveURL(/\/$/)
})

test('automatic detection proposes the cuts and they stay editable', async ({ page }) => {
  await loginAsUser(page)
  await page.goto('/upload/split')

  await page
    .locator('input[type="file"]')
    .setInputFiles(pdfUpload('scan.pdf', uniqueScan(4, 'detect')))
  await expect(page.getByText('4 pages. Click a gap between pages to cut there.')).toBeVisible()

  await page.getByRole('button', { name: 'Detect automatically' }).click()

  // The mock model proposes pages 1 and 2 as their own documents; the pages it
  // forgot are covered by the trailing part.
  await expect(page.getByText('Proposed 3 documents')).toBeVisible()
  await expect(page.getByText('3 documents: pages 1, 2, 3–4')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Split after page 1' })).toHaveAttribute(
    'aria-pressed',
    'true',
  )

  // The proposal is a starting point, not a decision.
  await page.getByRole('button', { name: 'Split after page 1' }).click()
  await expect(page.getByText('2 documents: pages 1–2, 3–4')).toBeVisible()
})

test('cancelling a staged PDF creates nothing', async ({ page }) => {
  await loginAsUser(page)
  await page.goto('/upload/split')

  await page
    .locator('input[type="file"]')
    .setInputFiles(pdfUpload('scan.pdf', uniqueScan(3, 'cancelled')))
  await expect(page.getByText('3 pages. Click a gap between pages to cut there.')).toBeVisible()

  await page.getByRole('button', { name: 'Cancel' }).click()
  await expect(page.getByRole('button', { name: 'Split into 1 document' })).toBeHidden()
  await expect(page.getByText('Choose a PDF to split')).toBeVisible()
})

test('a PDF with nothing to split is rejected', async ({ page }) => {
  await loginAsUser(page)
  await page.goto('/upload/split')

  await page
    .locator('input[type="file"]')
    .setInputFiles(pdfUpload('one-page.pdf', multiPagePdf(1)))
  await expect(
    page.getByText(
      'This PDF has only one page, so there is nothing to split. Use the Files tab to upload it.',
    ),
  ).toBeVisible()

  await page.locator('input[type="file"]').setInputFiles({
    name: 'notes.pdf',
    mimeType: 'application/pdf',
    buffer: Buffer.from('this is not a pdf at all'),
  })
  await expect(page.getByText('The upload is not a readable PDF.')).toBeVisible()
  await expect(page.getByText('Choose a PDF to split')).toBeVisible()
})
