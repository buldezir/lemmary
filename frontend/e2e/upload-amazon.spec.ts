import { test, expect } from '@playwright/test'
import { loginAsUser } from './helpers/auth'
import { amazonExportZip, uniqueInvoice } from './helpers/zip'

function archiveUpload(name: string, buffer: Buffer) {
  return { name, mimeType: 'application/zip', buffer }
}

test('amazon export is previewed and only imported after confirming', async ({ page }) => {
  await loginAsUser(page)
  await page.goto('/upload/amazon')
  await expect(page.getByRole('heading', { name: 'Import Amazon orders' })).toBeVisible()

  const archive = amazonExportZip([uniqueInvoice('one'), uniqueInvoice('two')])
  await page
    .locator('input[type="file"]')
    .setInputFiles(archiveUpload('Your Orders.zip', archive))

  // Only the PDFs count; the CSV report and delivery photo are ignored.
  await expect(page.getByText('Found 2 PDF files: 2 new')).toBeVisible()
  await expect(page.getByText('2 other files in the archive ignored.')).toBeVisible()
  await expect(
    page.getByText('Do you actually want to import 2 files (duplicates will be ignored)?'),
  ).toBeVisible()

  await page.getByRole('button', { name: 'Import 2 files' }).click()
  await expect(page.getByText('Imported 2 documents.')).toBeVisible()

  // The same archive again is recognised as already imported.
  await page
    .locator('input[type="file"]')
    .setInputFiles(archiveUpload('Your Orders.zip', archive))
  await expect(page.getByText('Found 2 PDF files: 0 new, 2 already in your library')).toBeVisible()
  await expect(page.getByText('Every PDF in this archive is already in your library.')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Import 0 files' })).toBeDisabled()
})

test('cancelling an amazon export imports nothing', async ({ page }) => {
  await loginAsUser(page)
  await page.goto('/upload/amazon')

  const archive = amazonExportZip([uniqueInvoice('cancelled')])
  await page.locator('input[type="file"]').setInputFiles(archiveUpload('orders.zip', archive))
  await expect(
    page.getByText('Do you actually want to import 1 file (duplicates will be ignored)?'),
  ).toBeVisible()

  await page.getByRole('button', { name: 'Cancel' }).click()
  await expect(page.getByRole('button', { name: 'Import 1 file' })).toBeHidden()
  await expect(page.getByText('Choose the order export (.zip)')).toBeVisible()

  // Nothing was created, so the same archive is still new on a second attempt.
  await page.locator('input[type="file"]').setInputFiles(archiveUpload('orders.zip', archive))
  await expect(page.getByText('Found 1 PDF file: 1 new')).toBeVisible()
})

test('a file that is not an order export is rejected', async ({ page }) => {
  await loginAsUser(page)
  await page.goto('/upload/amazon')

  await page.locator('input[type="file"]').setInputFiles({
    name: 'notes.zip',
    mimeType: 'application/zip',
    buffer: Buffer.from('this is not a zip archive'),
  })
  await expect(page.getByText('The upload is not a readable zip archive.')).toBeVisible()
  await expect(page.getByText('Choose the order export (.zip)')).toBeVisible()
})
