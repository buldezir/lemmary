import { pb, pbUrl } from './pb'
import { ensureAuth } from './auth'

type ApiFetchOptions = {
  method?: 'GET' | 'POST' | 'PATCH' | 'DELETE'
  /** JSON request body; mutually exclusive with `formData`. */
  body?: unknown
  formData?: FormData
  /** Skip auth entirely (setup and meta endpoints are public). */
  public?: boolean
  /** Error shown when the server response carries no `detail`. */
  fallbackError: string
}

async function readJson(response: Response): Promise<unknown> {
  try {
    return await response.json()
  } catch {
    return null // some error responses are not JSON
  }
}

export function errorDetail(data: unknown, fallback: string): string {
  const detail = (data as { detail?: unknown } | null)?.detail
  return typeof detail === 'string' && detail ? detail : fallback
}

/**
 * What the user is told when a plain request never made it.
 *
 * Deliberately says nothing about what happens next: `apiFetch` carries
 * documents, settings, imports and passkeys, and a POST that failed on the
 * wire may or may not have been applied. Only the search stream can promise
 * more, and it does -- see `streamConnectionLostMessage`.
 */
export const connectionLostMessage = 'Could not reach the server. Check your connection and try again.'

/**
 * The same failure on a search stream, where more is known.
 *
 * A run outlives its connection: the server finishes it and stores the turn
 * whether or not anyone is still reading. So losing the stream is not losing
 * the answer, and saying so is the difference between a user who waits and one
 * who pays for the same research twice.
 */
export const streamConnectionLostMessage =
  'The connection to the server was interrupted. The run continues, and its answer will be in your chat history.'

/**
 * A stream that broke after the run had already started.
 *
 * Typed rather than a plain Error because callers must treat it differently
 * from a send that failed: the question reached the server and is being
 * answered, so putting it back in the composer invites the user to pay for the
 * same run twice.
 */
export class StreamInterruptedError extends Error {
  constructor(cause: unknown) {
    super(streamConnectionLostMessage, { cause })
    this.name = 'StreamInterruptedError'
  }
}

/**
 * Turns a transport failure into something worth reading.
 *
 * A request that dies on the wire surfaces as whatever the browser calls it
 * that week — "Failed to fetch" in Chrome, "Error in input stream" when it is
 * the response body that breaks mid-read, "NetworkError…" in Firefox — and
 * those went straight into the page. None of them tell the user the one thing
 * that matters, which is that the run may well have finished anyway.
 *
 * Only transport failures: a DOMException from an abort is the caller's to
 * interpret, and an Error we raised ourselves already carries the server's own
 * wording.
 */
export function isConnectionError(err: unknown): boolean {
  return err instanceof TypeError
}

/**
 * Calls a custom `/api/app` endpoint: attaches the session token, JSON-encodes
 * the body, and turns non-2xx responses into Errors carrying the server's
 * `detail` message.
 */
export async function apiFetch<T>(path: string, options: ApiFetchOptions): Promise<T> {
  const { method = 'GET', body, formData, public: isPublic = false, fallbackError } = options
  if (!isPublic) {
    await ensureAuth()
  }

  const headers: Record<string, string> = {}
  if (!isPublic) {
    headers.Authorization = pb.authStore.token
  }
  if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
  }

  let response: Response
  try {
    response = await fetch(`${pbUrl}${path}`, {
      method,
      headers,
      body: formData ?? (body !== undefined ? JSON.stringify(body) : undefined),
    })
  } catch (err) {
    if (isConnectionError(err)) {
      throw new Error(connectionLostMessage, { cause: err })
    }
    throw err
  }

  const data = await readJson(response)
  if (!response.ok) {
    throw new Error(errorDetail(data, fallbackError))
  }
  return data as T
}

/**
 * Splits an SSE byte stream into event payloads. Kept separate from the fetch
 * plumbing because the boundary cases — a frame split across two chunks, a
 * trailing partial frame — are what actually break, and they are worth testing
 * without a server.
 */
export function createSSEParser(onEvent: (payload: string) => void) {
  let buffer = ''
  return {
    push(chunk: string) {
      buffer += chunk
      let boundary = buffer.indexOf('\n\n')
      while (boundary !== -1) {
        const frame = buffer.slice(0, boundary)
        buffer = buffer.slice(boundary + 2)
        for (const line of frame.split('\n')) {
          if (line.startsWith('data: ')) {
            onEvent(line.slice(6))
          }
        }
        boundary = buffer.indexOf('\n\n')
      }
    },
  }
}

type ApiStreamOptions<TEvent> = {
  body: unknown
  onEvent: (event: TEvent) => void
  signal?: AbortSignal
  fallbackError: string
}

/**
 * POSTs to an endpoint that answers with server-sent events and delivers each
 * one to onEvent. It is a POST because the request carries the conversation, so
 * EventSource (GET-only) is not an option.
 */
export async function apiStream<TEvent>(path: string, options: ApiStreamOptions<TEvent>) {
  await ensureAuth()

  let response: Response
  try {
    response = await fetch(`${pbUrl}${path}`, {
      method: 'POST',
      headers: {
        Authorization: pb.authStore.token,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify(options.body),
      signal: options.signal,
    })
  } catch (err) {
    // The generic message, not the stream's: this request never connected, so
    // there is no run on the other side to promise anything about.
    if (isConnectionError(err)) {
      throw new Error(connectionLostMessage, { cause: err })
    }
    throw err
  }

  if (!response.ok) {
    throw new Error(errorDetail(await readJson(response), options.fallbackError))
  }
  if (!response.body) {
    throw new Error(options.fallbackError)
  }

  const parser = createSSEParser((payload) => {
    try {
      options.onEvent(JSON.parse(payload) as TEvent)
    } catch {
      // A malformed frame is not worth failing the whole run over.
    }
  })

  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  for (;;) {
    let chunk: ReadableStreamReadResult<Uint8Array>
    try {
      chunk = await reader.read()
    } catch (err) {
      // The body broke mid-stream. Chrome reports this as
      // `TypeError: Error in input stream`, which is not something to show
      // anyone; the run itself may well be finishing on the server.
      if (isConnectionError(err)) {
        throw new StreamInterruptedError(err)
      }
      throw err
    }
    if (chunk.done) break
    parser.push(decoder.decode(chunk.value, { stream: true }))
  }
}

export type JobProgress = {
  done: number
  total: number
}

type JobStatusResponse = {
  status?: string
  progress?: JobProgress
  error?: string
  result?: unknown
  detail?: string
}

const jobPollIntervalMs = 500

/**
 * How long a job may run before the client gives up on it. Only a safety net:
 * a caller whose backend job can legitimately run longer passes its own budget,
 * because reporting a failure while the server keeps working is worse than
 * waiting.
 */
const defaultJobTimeoutMs = 5 * 60 * 1000

export type PollJobOptions = {
  onProgress?: (progress: JobProgress) => void
  /** How long to keep polling before giving up. */
  timeoutMs?: number
  /** What the run is called in error messages ("import", "split", …). */
  label?: string
}

function sleep(ms: number) {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

/**
 * Polls a job-status endpoint until the job finishes. Network errors and 5xx
 * responses are retried; 4xx responses and malformed payloads abort.
 */
export async function pollJob<TResult>(
  statusPath: string,
  opts: PollJobOptions = {},
): Promise<TResult> {
  const label = opts.label ?? 'job'
  const attempts = Math.ceil((opts.timeoutMs ?? defaultJobTimeoutMs) / jobPollIntervalMs)

  for (let attempt = 0; attempt < attempts; attempt++) {
    let response: Response
    try {
      response = await fetch(`${pbUrl}${statusPath}`, {
        headers: { Authorization: pb.authStore.token },
      })
    } catch {
      await sleep(jobPollIntervalMs)
      continue
    }
    if (response.status >= 500) {
      await sleep(jobPollIntervalMs)
      continue
    }

    const data = (await readJson(response)) as JobStatusResponse | null
    if (data === null) {
      throw new Error(`Failed to poll the ${label} status`)
    }
    if (!response.ok) {
      throw new Error(errorDetail(data, `Failed to poll the ${label} status`))
    }
    if (data.progress) {
      opts.onProgress?.(data.progress)
    }
    if (data.status === 'completed') {
      if (data.result == null) {
        throw new Error(`The ${label} completed without a result`)
      }
      return data.result as TResult
    }
    if (data.status === 'failed') {
      throw new Error(data.error ?? `The ${label} failed`)
    }
    await sleep(jobPollIntervalMs)
  }

  throw new Error(`The ${label} is taking longer than expected; it may still be running on the server`)
}
