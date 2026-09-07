import { describe, expect, it } from 'vitest'
import {
  connectionLostMessage,
  createSSEParser,
  isConnectionError,
  StreamInterruptedError,
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
    // ": ping" is what the research stream sends while a model completion is in
    // flight, so an idle-timeout proxy does not drop the connection. It is not
    // an event and must never reach onEvent.
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
    // It reached the page verbatim, telling the user nothing except that
    // something called an input stream exists.
    expect(isConnectionError(new TypeError('Error in input stream'))).toBe(true)
  })

  it('recognises a request that never connected', () => {
    expect(isConnectionError(new TypeError('Failed to fetch'))).toBe(true)
  })

  it('leaves an aborted request alone', () => {
    // Cancelling is the caller's to phrase -- "Research cancelled." rather
    // than anything about the connection.
    expect(isConnectionError(new DOMException('The user aborted a request.', 'AbortError'))).toBe(
      false,
    )
  })

  it('leaves an error we raised ourselves alone', () => {
    // Already carries the server's own `detail` wording.
    expect(isConnectionError(new Error('This chat is a research chat.'))).toBe(false)
  })
})

describe('connection-lost copy', () => {
  it('promises nothing about saved answers on a plain request', () => {
    // apiFetch carries documents, settings, imports and passkeys. Telling
    // someone their settings save "is in your chat history" is nonsense, and
    // a POST that died on the wire may or may not have been applied.
    expect(connectionLostMessage).not.toMatch(/chat history/i)
    expect(connectionLostMessage).not.toMatch(/answer/i)
  })

  it('tells a dropped search stream where its answer went', () => {
    // Here it is knowable: the run outlives the connection and stores its
    // turn, so the user should wait rather than pay for a second run.
    expect(streamConnectionLostMessage).toMatch(/chat history/i)
  })

  it('marks an interrupted stream as its own kind of failure', () => {
    // The composer restore keys off this: a question already being answered
    // must not be handed back as if it had never been sent.
    const err = new StreamInterruptedError(new TypeError('Error in input stream'))
    expect(err).toBeInstanceOf(StreamInterruptedError)
    expect(err.message).toBe(streamConnectionLostMessage)
  })
})
