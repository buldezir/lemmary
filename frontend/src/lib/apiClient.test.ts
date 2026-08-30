import { describe, expect, it } from 'vitest'
import { createSSEParser } from './apiClient'

function collect(chunks: string[]) {
  const seen: string[] = []
  const parser = createSSEParser((payload) => seen.push(payload))
  for (const chunk of chunks) {
    parser.push(chunk)
  }
  return seen
}

describe('createSSEParser', () => {
  it('emits each complete frame', () => {
    expect(collect(['data: {"type":"step"}\n\ndata: {"type":"done"}\n\n'])).toEqual([
      '{"type":"step"}',
      '{"type":"done"}',
    ])
  })

  it('reassembles a frame split across chunks', () => {
    // The network splits wherever it likes; a research step must not be lost
    // because its JSON straddled two reads.
    expect(collect(['data: {"type":"st', 'ep","kind":"search"}\n', '\n'])).toEqual([
      '{"type":"step","kind":"search"}',
    ])
  })

  it('holds back a trailing partial frame', () => {
    expect(collect(['data: {"type":"delta"}\n\ndata: {"incomp'])).toEqual(['{"type":"delta"}'])
  })

  it('ignores comments, blank frames and non-data lines', () => {
    expect(collect([': keep-alive\n\ndata: {"type":"done"}\n\n'])).toEqual(['{"type":"done"}'])
  })

  it('reads every data line of a multi-line frame', () => {
    expect(collect(['event: step\ndata: {"a":1}\n\n'])).toEqual(['{"a":1}'])
  })
})
