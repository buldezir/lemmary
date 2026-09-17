import { pb, pbUrl } from './pb'
import { ensureAuth } from './auth'

type ApiFetchOptions = {
  method?: 'GET' | 'POST' | 'PATCH' | 'DELETE'
  /** JSON request body; mutually exclusive with `formData`. */
  body?: unknown
  formData?: FormData
  /** Skip auth entirely (setup and meta endpoints are public). */
  public?: boolean
  /** Cancels the request when the caller no longer owns the result. */
  signal?: AbortSignal
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
 * Deliberately says nothing about what happens next: a POST that failed on the
 * wire may or may not have been applied. Only the search stream can promise
 * more, in `streamConnectionLostMessage`.
 */
export const connectionLostMessage = 'Could not reach the server. Check your connection and try again.'

/**
 * The same failure on a search stream, where more is known: a run outlives its
 * connection, so losing the stream is not losing the answer, and saying so is
 * what keeps a user from paying for the same research twice.
 */
export const streamConnectionLostMessage =
  'The connection to the server was interrupted. The run continues, and its answer will be in your chat history.'

/**
 * A request that never made it over the wire. Typed so the chat surfaces, whose
 * runs are detached from the connection, can wait for the turn instead of
 * reporting a loss; every other caller just shows the message.
 */
export class ConnectionLostError extends Error {
  constructor(cause: unknown) {
    super(connectionLostMessage, { cause })
    this.name = 'ConnectionLostError'
  }
}

/**
 * A response the server answered with a failure status. Carries the status so
 * a poll can tell a 5xx worth retrying from a 4xx that ends the wait, and
 * `answered`, which says the body was the app's own `{"detail"}`: the handler
 * ran and finished, as opposed to a proxy giving up on a server still working.
 */
export class HttpError extends Error {
  status: number
  answered: boolean
  constructor(status: number, message: string, answered = false) {
    super(message)
    this.name = 'HttpError'
    this.status = status
    this.answered = answered
  }
}

function hasDetail(data: unknown): boolean {
  return typeof (data as { detail?: unknown } | null)?.detail === 'string'
}

/**
 * A request that broke once a run was already under way. The question reached
 * the server and is being answered, so putting it back in the composer would
 * invite the user to pay for the same run twice.
 */
export class RunInFlightError extends Error {
  constructor(cause: unknown) {
    super(streamConnectionLostMessage, { cause })
    this.name = 'RunInFlightError'
  }
}

/**
 * A request that dies on the wire surfaces as whatever the browser calls it
 * that week ("Failed to fetch", "Error in input stream", "NetworkError"), none
 * of which say the run may have finished anyway.
 *
 * Only transport failures: a DOMException from an abort is the caller's to
 * interpret, and an Error we raised carries the server's own wording.
 */
export function isConnectionError(err: unknown): boolean {
  return err instanceof TypeError
}

export async function apiFetch<T>(path: string, options: ApiFetchOptions): Promise<T> {
  const {
    method = 'GET',
    body,
    formData,
    public: isPublic = false,
    signal,
    fallbackError,
  } = options
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
      signal,
    })
  } catch (err) {
    if (isConnectionError(err)) {
      throw new ConnectionLostError(err)
    }
    throw err
  }

  const data = await readJson(response)
  if (!response.ok) {
    throw new HttpError(response.status, errorDetail(data, fallbackError), hasDetail(data))
  }
  return data as T
}

/**
 * Separate from the fetch plumbing so the boundary cases, a frame split across
 * chunks and a trailing partial frame, are testable without a server.
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
 * A POST because the request carries the conversation, so EventSource
 * (GET-only) is not an option.
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
      throw new ConnectionLostError(err)
    }
    throw err
  }

  if (!response.ok) {
    const data = await readJson(response)
    throw new HttpError(response.status, errorDetail(data, options.fallbackError), hasDetail(data))
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
      // The body broke mid-stream, which Chrome reports as a bare TypeError;
      // the run itself may well be finishing on the server.
      if (isConnectionError(err)) {
        throw new RunInFlightError(err)
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
 * Only a safety net: a caller whose job legitimately runs longer passes its own
 * budget, because reporting a failure while the server works is worse.
 */
const defaultJobTimeoutMs = 5 * 60 * 1000

export type PollJobOptions = {
  onProgress?: (progress: JobProgress) => void
  /** How long to keep polling before giving up. */
  timeoutMs?: number
  /** What the run is called in error messages ("import", "split", …). */
  label?: string
}

/** Exported so other polling loops (a run recovering from a dropped stream) share it. */
export function sleep(ms: number, signal?: AbortSignal) {
  if (!signal) {
    return new Promise<void>((resolve) => setTimeout(resolve, ms))
  }
  if (signal.aborted) {
    return Promise.resolve()
  }
  return new Promise<void>((resolve) => {
    const timer = setTimeout(done, ms)
    signal.addEventListener('abort', done, { once: true })

    function done() {
      clearTimeout(timer)
      signal?.removeEventListener('abort', done)
      resolve()
    }
  })
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
