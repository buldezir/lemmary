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

  const response = await fetch(`${pbUrl}${path}`, {
    method,
    headers,
    body: formData ?? (body !== undefined ? JSON.stringify(body) : undefined),
  })

  const data = await readJson(response)
  if (!response.ok) {
    throw new Error(errorDetail(data, fallbackError))
  }
  return data as T
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
