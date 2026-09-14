import { ClientResponseError } from 'pocketbase'
import { pb } from '../pb'
import { ensureAuth } from '../auth'

export type TagRecord = {
  id: string
  name: string
  user?: string
}

/**
 * The tag vocabulary, straight through the PocketBase SDK.
 *
 * No Go endpoint sits in front of this: the tags collection has been scoped to
 * `user = @request.auth.id` on all five rules since tags became per-user, so
 * the caller can only ever see and write their own. A wrapper would add a hop
 * and one more place for the ownership rule to drift.
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

/**
 * Turns the unique (user, name) index violation into something a person can
 * act on. PocketBase reports it as a 400 with a per-field validation code, and
 * the raw message ("Value must be unique.") never says which value.
 *
 * Exported for its test: the rest of this module is SDK calls, and this is the
 * only part with a decision in it.
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
