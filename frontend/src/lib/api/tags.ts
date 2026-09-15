import { ClientResponseError } from 'pocketbase'
import { pb } from '../pb'
import { ensureAuth } from '../auth'
import { apiFetch, pollJob, type JobProgress } from '../apiClient'

export type TagRecord = {
  id: string
  name: string
  user?: string
}

/**
 * The tag vocabulary, straight through the PocketBase SDK. No Go endpoint in
 * front: all five collection rules are scoped to `user = @request.auth.id`, and
 * a wrapper would be one more place for that to drift.
 */
export async function listTags(): Promise<TagRecord[]> {
  await ensureAuth()
  return pb.collection('tags').getFullList<TagRecord>({ sort: 'name' })
}

export async function createTag(name: string): Promise<TagRecord> {
  await ensureAuth()
  const userId = pb.authStore.record?.id
  if (!userId) {
    throw new Error('You must be signed in to create a tag.')
  }
  try {
    return await pb.collection('tags').create<TagRecord>({ name, user: userId })
  } catch (err) {
    throw duplicateNameError(err, name)
  }
}

export async function renameTag(id: string, name: string): Promise<TagRecord> {
  await ensureAuth()
  try {
    return await pb.collection('tags').update<TagRecord>(id, { name })
  } catch (err) {
    throw duplicateNameError(err, name)
  }
}

export async function deleteTag(id: string): Promise<void> {
  await ensureAuth()
  await pb.collection('tags').delete(id)
}

export type TagAssignPreview = {
  candidates: number
  limit: number
  running: boolean
}

/** How many documents lack this tag, so the button can price itself first. */
export function previewTagAssign(tagId: string): Promise<TagAssignPreview> {
  return apiFetch<TagAssignPreview>(`/api/app/tags/${encodeURIComponent(tagId)}/assign`, {
    fallbackError: 'Failed to count documents',
  })
}

export type TagAssignResult = {
  candidates: number
  asked: number
  assigned: number
  declined: number
  skipped: number
  failed: number
  errors?: string[]
  prompt_tokens: number
  completion_tokens: number
}

/**
 * One AI call per document. Without `documentIds` the server picks the
 * documents that lack the single tag given; with them it asks about the whole
 * vocabulary passed. Either way it only ever adds tags.
 */
export async function assignTagsWithAI(
  tagIds: string[],
  documentIds?: string[],
  onProgress?: (progress: JobProgress) => void,
): Promise<TagAssignResult> {
  const start = await apiFetch<{ job_id?: string }>('/api/app/tags/assign', {
    method: 'POST',
    body: { tag_ids: tagIds, document_ids: documentIds ?? [] },
    fallbackError: 'Tag assignment failed to start',
  })
  if (!start.job_id) {
    throw new Error('Tag assignment job id missing from server response')
  }

  const result = await pollJob<TagAssignResult>(
    `/api/app/tags/assign/status?job_id=${encodeURIComponent(start.job_id)}`,
    { label: 'tag assignment', onProgress },
  )
  return { ...result, errors: result.errors ?? [] }
}

/**
 * PocketBase reports the unique (user, name) violation as a 400 with a per-field
 * validation code, whose message ("Value must be unique.") never says which
 * value. Exported for its test.
 */
export function duplicateNameError(err: unknown, name: string): Error {
  if (err instanceof ClientResponseError) {
    const field = (err.response?.data as Record<string, { code?: string }> | undefined)?.name
    if (field?.code === 'validation_not_unique') {
      return new Error(`You already have a tag called "${name}".`)
    }
  }
  return err instanceof Error ? err : new Error('The tag could not be saved.')
}
