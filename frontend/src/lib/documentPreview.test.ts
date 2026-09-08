import { describe, expect, it } from 'vitest'
import { previewKind } from './documentPreview'

describe('previewKind', () => {
  it('frames a PDF whatever the case of its extension', () => {
    expect(previewKind('scan_ab12cd34ef.pdf')).toBe('pdf')
    expect(previewKind('SCAN.PDF')).toBe('pdf')
  })

  it('recognises the image types the upload field accepts', () => {
    for (const name of ['a.jpg', 'a.jpeg', 'a.png', 'a.webp', 'weird.name.PNG']) {
      expect(previewKind(name)).toBe('image')
    }
  })

  // txt and csv would in fact render in a frame -- their text is already on the
  // page in the OCR-text field, so they are left out on purpose.
  it('has nothing to show for the remaining upload types', () => {
    for (const name of ['notes.txt', 'rows.csv', 'letter.docx', 'sheet.xlsx']) {
      expect(previewKind(name)).toBe('none')
    }
  })

  // The last extension is the one the bytes were sniffed against.
  it('reads only the last extension', () => {
    expect(previewKind('report.pdf.docx')).toBe('none')
    expect(previewKind('report.docx.pdf')).toBe('pdf')
  })

  it('gives up on a name with no extension', () => {
    for (const name of ['', 'invoice', 'invoice.']) {
      expect(previewKind(name)).toBe('none')
    }
  })
})
