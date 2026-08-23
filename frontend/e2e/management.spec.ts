import { test, expect, type Page } from '@playwright/test'
import { loginAsSuper } from './helpers/auth'

// The failed count comes straight off the documents collection, so a stub is
// what lets these specs assert the section without failing real documents.
async function stubFailedCount(page: Page, totalItems: number) {
  await page.route('**/api/collections/documents/records?**', (route) => {
    const filter = new URL(route.request().url()).searchParams.get('filter') ?? ''
    if (!filter.includes('failed')) return route.fallback()
    return route.fulfill({
      json: { page: 1, perPage: 1, totalItems, totalPages: totalItems, items: [] },
    })
  })
}

test('failed processing section reports the count and offers a capped batch', async ({ page }) => {
  await loginAsSuper(page)
  await stubFailedCount(page, 342)
  await page.goto('/management')

  await expect(page.getByRole('heading', { name: 'Failed processing' })).toBeVisible()
  await expect(page.getByText('342 documents have failed processing.')).toBeVisible()

  // Default batch is 100, so the button offers 100 of the 342 - not all of them.
  const reprocess = page.getByRole('button', { name: 'Reprocess 100 failed' })
  await expect(reprocess).toBeEnabled()
  await expect(reprocess).toHaveCSS('cursor', 'pointer')

  await page.getByLabel('Batch').selectOption('500')
  await expect(page.getByRole('button', { name: 'Reprocess 342 failed' })).toBeVisible()
})

test('failed processing is disabled when nothing has failed', async ({ page }) => {
  await loginAsSuper(page)
  await stubFailedCount(page, 0)
  await page.goto('/management')

  await expect(page.getByText('No documents have failed processing.')).toBeVisible()
  const reprocess = page.getByRole('button', { name: /^Reprocess 0 failed$/ })
  await expect(reprocess).toBeDisabled()
  await expect(reprocess).toHaveCSS('cursor', 'not-allowed')
})

test('reprocess sweep sends the chosen batch size and mode', async ({ page }) => {
  await loginAsSuper(page)
  await stubFailedCount(page, 342)

  let payload: unknown = null
  await page.route('**/api/app/documents/reprocess-failed', (route) => {
    payload = route.request().postDataJSON()
    return route.fulfill({ json: { queued: 50, skipped: 0, remaining: 292 } })
  })

  await page.goto('/management')
  await page.getByLabel('Batch').selectOption('50')
  await page.getByLabel('Steps').selectOption('full')

  page.once('dialog', (dialog) => {
    expect(dialog.message()).toContain('Reprocess 50 failed documents?')
    expect(dialog.message()).toContain('Full pipeline')
    void dialog.accept()
  })
  await page.getByRole('button', { name: 'Reprocess 50 failed' }).click()

  await expect(
    page.getByText('Queued 50 documents. 292 documents still failed', { exact: false }),
  ).toBeVisible()
  expect(payload).toEqual({ limit: 50, mode: 'full' })

  // The remaining count from the response drives the next batch's label.
  await expect(page.getByText('292 documents have failed processing.')).toBeVisible()
})

test('cancelling the confirm does not queue anything', async ({ page }) => {
  await loginAsSuper(page)
  await stubFailedCount(page, 5)

  let called = false
  await page.route('**/api/app/documents/reprocess-failed', (route) => {
    called = true
    return route.fulfill({ json: { queued: 5, skipped: 0, remaining: 0 } })
  })

  await page.goto('/management')
  page.once('dialog', (dialog) => void dialog.dismiss())
  await page.getByRole('button', { name: 'Reprocess 5 failed' }).click()

  await expect(page.getByText('5 documents have failed processing.')).toBeVisible()
  expect(called).toBe(false)
})
