/**
 * Builds a small multi-page PDF in memory, so the split tests can create a
 * scan-shaped file without committing a binary fixture. Mirrors the backend's
 * internal/pdftool/testpdf generator.
 */
export function multiPagePdf(pageCount: number, ...extraLines: string[]): Buffer {
  const pages = Math.max(1, pageCount)
  const firstPageObj = 4

  const kids = Array.from({ length: pages }, (_, i) => `${firstPageObj + 2 * i} 0 R`).join(' ')
  const objects = [
    '<< /Type /Catalog /Pages 2 0 R >>',
    `<< /Type /Pages /Kids [${kids}] /Count ${pages} >>`,
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>',
  ]

  for (let i = 0; i < pages; i++) {
    const contentObj = firstPageObj + 2 * i + 1
    objects.push(
      '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] ' +
        `/Resources << /Font << /F1 3 0 R >> >> /Contents ${contentObj} 0 R >>`,
    )
    objects.push(contentStream([`Page ${i + 1}`, ...extraLines]))
  }

  return assemble(objects)
}

function contentStream(lines: string[]): string {
  const body =
    'BT /F1 18 Tf 72 760 Td 24 TL\n' +
    lines.map((line) => `(${escapeText(line)}) Tj T*\n`).join('') +
    'ET\n'
  return `<< /Length ${Buffer.byteLength(body, 'latin1')} >>\nstream\n${body}\nendstream`
}

function escapeText(s: string): string {
  return s.replace(/[()\\]/g, (match) => `\\${match}`)
}

/**
 * Writes the objects with a cross-reference table whose offsets come from the
 * bytes actually emitted, which is what makes the result readable by poppler.
 */
function assemble(objects: string[]): Buffer {
  let body = '%PDF-1.4\n'
  const offsets: number[] = []
  objects.forEach((object, index) => {
    offsets.push(Buffer.byteLength(body, 'latin1'))
    body += `${index + 1} 0 obj\n${object}\nendobj\n`
  })

  const xrefOffset = Buffer.byteLength(body, 'latin1')
  body += `xref\n0 ${objects.length + 1}\n0000000000 65535 f \n`
  for (const offset of offsets) {
    body += `${String(offset).padStart(10, '0')} 00000 n \n`
  }
  body += `trailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\nstartxref\n${xrefOffset}\n%%EOF\n`

  return Buffer.from(body, 'latin1')
}

/** A PDF whose page text is unique to this call, so runs do not collide. */
export function uniqueScan(pageCount: number, marker: string): Buffer {
  return multiPagePdf(pageCount, `Scan ${marker}-${Date.now()}`, 'Acme Plumbing GmbH')
}
