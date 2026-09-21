import { ClientResponseError } from 'pocketbase'
import { pb } from '../pb'
import { ensureAuth } from '../auth'
import { apiFetch, pollJob } from '../apiClient'
import { notifyDocumentsChanged } from '../documentEvents'
import { tagKey } from '../tagSuggestions'

export type TagRecord = {
  id: string
  name: string
  /** `#rrggbb`, or empty for the neutral border every tag had before colours. */
  color?: string
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

/**
 * The one place a tag is created outside /tags: accepting an AI suggestion on
 * a document awaiting review. Reuses a tag of the same name if one exists, then
 * appends it to the document with the `tags+` modifier, so a stale client copy
 * of the relation is never written back over tags added meanwhile. The status
 * is left alone; "Mark reviewed" is still the reviewer's call.
 */
export async function acceptSuggestedTag(documentId: string, name: string): Promise<TagRecord> {
  const key = tagKey(name)
  const tag = (await listTags()).find((t) => tagKey(t.name) === key) ?? (await createTag(name))
  await pb.collection('documents').update(documentId, { 'tags+': tag.id })
  notifyDocumentsChanged()
  return tag
}

export async function renameTag(id: string, name: string): Promise<TagRecord> {
  await ensureAuth()
  try {
    return await pb.collection('tags').update<TagRecord>(id, { name })
  } catch (err) {
    throw duplicateNameError(err, name)
  }
}

export async function setTagColor(id: string, color: string): Promise<TagRecord> {
  await ensureAuth()
  return pb.collection('tags').update<TagRecord>(id, { color })
}

export async function deleteTag(id: string): Promise<void> {
  await ensureAuth()
  await pb.collection('tags').delete(id)
}

/**
 * Mirrors maxTagAssignDocuments in backend/internal/appapi/tags_assign.go. Read
 * only to word the cost block before any preview has been fetched; every run
 * takes the limit the server reports back.
 */
export const MAX_TAG_ASSIGN_DOCUMENTS = 1000

export type TagAssignPreview = {
  candidates: number
  limit: number
  running: boolean
}

const assignQuery = (tagIds: string[]) =>
  `tag_ids=${encodeURIComponent(tagIds.join(','))}`

/**
 * How many documents are missing at least one of these tags, so the button can
 * price itself first. One number, because one pass covers them all.
 */
export function previewTagAssign(tagIds: string[]): Promise<TagAssignPreview> {
  return apiFetch<TagAssignPreview>(`/api/app/tags/assign?${assignQuery(tagIds)}`, {
    fallbackError: 'Failed to count documents',
  })
}

export type TagAssignResult = {
  candidates: number
  asked: number
  assigned: number
  declined: number
  failed: number
  errors?: string[]
  prompt_tokens: number
  completion_tokens: number
}

/**
 * Asks the model which of these tags apply to each document missing any of
 * them, and only ever adds: nothing else on a document changes.
 *
 * Several tags cost what one costs. The prompt is mostly the document's own
 * text, so one pass offering every name beats a pass per name, which would
 * re-read the whole archive each time.
 */
export async function assignTagsWithAI(tagIds: string[]): Promise<TagAssignResult> {
  const start = await apiFetch<{ job_id?: string }>(`/api/app/tags/assign?${assignQuery(tagIds)}`, {
    method: 'POST',
    fallbackError: 'Tag assignment failed to start',
  })
  if (!start.job_id) {
    throw new Error('Tag assignment job id missing from server response')
  }

  const result = await pollJob<TagAssignResult>(
    `/api/app/tags/assign/status?job_id=${encodeURIComponent(start.job_id)}`,
    { label: 'tag assignment' },
  )
  // The server wrote the tags, so nothing on this side has seen them: without
  // this the cards keep their old chips beside a success message.
  notifyDocumentsChanged()
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
