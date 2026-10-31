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
})
