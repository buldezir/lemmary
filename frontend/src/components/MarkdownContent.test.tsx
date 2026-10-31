import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { MarkdownContent } from './MarkdownContent'

const ocr = '# Invoice\n\n<table><tr><td onclick="alert(1)">42</td></tr></table>\n\n<script>alert(1)</script>'

describe('MarkdownContent', () => {
  it('escapes embedded HTML by default', () => {
    const html = renderToStaticMarkup(<MarkdownContent content={ocr} />)
    expect(html).not.toContain('<table')
    expect(html).not.toContain('<script')
  })

  it('renders embedded HTML sanitized when asked', () => {
    const html = renderToStaticMarkup(<MarkdownContent content={ocr} html />)
    expect(html).toContain('Invoice</h1>')
    expect(html).toContain('<td class="border-t border-line px-2 py-1">42</td>')
    expect(html).not.toContain('onclick')
    expect(html).not.toContain('<script')
  })

  it('keeps merged table cells', () => {
    const html = renderToStaticMarkup(
      <MarkdownContent
        content={'<table><tr><th colspan="2" scope="col">Total</th></tr><tr><td rowspan="3" align="center">A</td></tr></table>'}
        html
      />,
    )
    expect(html).toContain('<th colSpan="2" scope="col"')
    expect(html).toContain('<td rowSpan="3" style="text-align:center"')
  })

  it('drops images, which would fetch any URL they name', () => {
    for (const src of ['https://example.test/pixel.png', '//example.test/x.png', '/api/documents/x']) {
      const html = renderToStaticMarkup(
        <MarkdownContent content={`<img src="${src}">\n\n![a](${src})\n\n<picture><source srcset="${src}"></picture>`} html />,
      )
      expect(html).not.toContain(src)
    }
  })
})
