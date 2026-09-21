import type { ProcessingJobRecord } from './processing'

/** Mirrors worker.NormalizeTagKey: lowercase, marks stripped, letters and digits only. */
export function tagKey(name: string): string {
  return name
    .normalize('NFD')
    .replace(/\p{M}/gu, '')
    .toLowerCase()
    .replace(/[^\p{L}\p{N}]/gu, '')
}

/**
 * The AI's proposed new tags still worth showing on a document awaiting review:
 * deduped, and minus any name the document already carries. Anything else about
 * the document's state is the caller's to check; a document that left
 * needs_review simply stops asking.
 */
export function pendingTagSuggestions(
  job: Pick<ProcessingJobRecord, 'metadata_json'> | null | undefined,
  assignedTagNames: string[],
): string[] {
  const taken = new Set(assignedTagNames.map(tagKey))
  const out: string[] = []
  for (const raw of job?.metadata_json?.suggested_tags ?? []) {
    const name = raw.trim()
    const key = tagKey(name)
    if (!key || taken.has(key)) continue
    taken.add(key)
    out.push(name)
  }
  return out
}
