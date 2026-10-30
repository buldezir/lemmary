import { afterEach, expect, it, vi } from 'vitest'
import { REVOKE_AFTER_MS, saveBlob } from './download'

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

it('keeps the blob URL alive until the browser has written the download', () => {
  vi.useFakeTimers()
  const anchor = { click: vi.fn(), remove: vi.fn() } as unknown as HTMLAnchorElement
  vi.stubGlobal('document', { createElement: () => anchor, body: { appendChild: vi.fn() } })
  vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:report')
  const revoke = vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})

  saveBlob(new Blob(['%PDF-']), 'Report.pdf')

  expect(anchor.click).toHaveBeenCalled()
  expect(anchor.download).toBe('Report.pdf')
  expect(revoke).not.toHaveBeenCalled()
  vi.advanceTimersByTime(REVOKE_AFTER_MS)
  expect(revoke).toHaveBeenCalledWith('blob:report')
})
