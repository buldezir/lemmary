import { describe, expect, it } from 'vitest'
import {
  connectionLostMessage,
  createSSEParser,
  isConnectionError,
  RunInFlightError,
  streamConnectionLostMessage,
} from './apiClient'

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
    expect(collect(['data: {"type":"st', 'ep","kind":"search"}\n', '\n'])).toEqual([
      '{"type":"step","kind":"search"}',
    ])
  })

  it('holds back a trailing partial frame', () => {
    expect(collect(['data: {"type":"delta"}\n\ndata: {"incomp'])).toEqual(['{"type":"delta"}'])
  })

  it('ignores comments, blank frames and non-data lines', () => {
    // ": ping" keeps an idle-timeout proxy from dropping the research stream.
    // It is not an event and must never reach onEvent.
    expect(collect([': keep-alive\n\n: ping\n\ndata: {"type":"done"}\n\n'])).toEqual([
      '{"type":"done"}',
    ])
  })

  it('reads every data line of a multi-line frame', () => {
    expect(collect(['event: step\ndata: {"a":1}\n\n'])).toEqual(['{"a":1}'])
  })
})

describe('isConnectionError', () => {
  it('recognises a body that broke mid-stream', () => {
    // What Chrome throws when the response body dies under an open reader.
    expect(isConnectionError(new TypeError('Error in input stream'))).toBe(true)
  })

  it('recognises a request that never connected', () => {
    expect(isConnectionError(new TypeError('Failed to fetch'))).toBe(true)
  })

  it('leaves an aborted request alone', () => {
    expect(isConnectionError(new DOMException('The user aborted a request.', 'AbortError'))).toBe(
      false,
    )
  })

  it('leaves an error we raised ourselves alone', () => {
    expect(isConnectionError(new Error('This chat is a research chat.'))).toBe(false)
  })
})

describe('connection-lost copy', () => {
  it('promises nothing about saved answers on a plain request', () => {
    // apiFetch also carries settings and imports, where "in your chat history"
    // would be nonsense.
    expect(connectionLostMessage).not.toMatch(/chat history/i)
    expect(connectionLostMessage).not.toMatch(/answer/i)
  })

  it('tells a dropped search stream where its answer went', () => {
    expect(streamConnectionLostMessage).toMatch(/chat history/i)
  })

  it('marks an interrupted stream as its own kind of failure', () => {
    // The composer restore keys off this class.
    const err = new RunInFlightError(new TypeError('Error in input stream'))
    expect(err).toBeInstanceOf(RunInFlightError)
    expect(err.message).toBe(streamConnectionLostMessage)
  })
})
