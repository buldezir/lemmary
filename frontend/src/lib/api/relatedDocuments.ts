import { pb } from '../pb'
import { notifyDocumentsChanged } from '../documentEvents'

export type RelatedDocument = {
  id: string
  title: string
  document_date: string
  created: string
}

const FIELDS = 'id,title,document_date,created'

const LINKS = ['related', 'documents_via_related'] as const

/**
 * Both directions: a link is stored on whichever of the two documents made it.
 * Expanded from the one record, not filtered over all documents: a filter on
 * documents_via_related joins the table to itself through json_each and timed
 * out on an archive of 9k documents.
 */
export async function listRelatedDocuments(documentId: string): Promise<RelatedDocument[]> {
  const record = await pb.collection('documents').getOne<{
    expand?: Partial<Record<(typeof LINKS)[number], RelatedDocument[]>>
  }>(documentId, {
    expand: LINKS.join(','),
    fields: LINKS.flatMap((link) => FIELDS.split(',').map((field) => `expand.${link}.${field}`)).join(','),
    requestKey: `related-${documentId}`,
  })
  const byId = new Map(LINKS.flatMap((link) => record.expand?.[link] ?? []).map((doc) => [doc.id, doc]))
  return [...byId.values()].sort((a, b) => b.document_date.localeCompare(a.document_date))
}

/**
 * The owner's own documents only: a link to one shared with them would be
 * refused. One request key, so each keystroke cancels the search before it.
 */
export async function searchLinkableDocuments(
  documentId: string,
  query: string,
): Promise<RelatedDocument[]> {
  const q = query.trim()
  const filter = 'user = {:me} && id != {:id}' + (q ? ' && title ~ {:q}' : '')
  const result = await pb.collection('documents').getList<RelatedDocument>(1, 20, {
    filter: pb.filter(filter, { me: pb.authStore.record?.id ?? '', id: documentId, q }),
    fields: FIELDS,
    sort: '-document_date',
    requestKey: 'related-search',
  })
  return result.items
}

export async function linkDocuments(documentId: string, relatedId: string): Promise<void> {
  await pb.collection('documents').update(documentId, { 'related+': relatedId }, { requestKey: null })
  notifyDocumentsChanged()
}

/** Removes the link from both sides, since either may hold it; a missing id is a no-op. */
export async function unlinkDocuments(documentId: string, relatedId: string): Promise<void> {
  await Promise.all([
    pb.collection('documents').update(documentId, { 'related-': relatedId }, { requestKey: null }),
    pb.collection('documents').update(relatedId, { 'related-': documentId }, { requestKey: null }),
  ])
  notifyDocumentsChanged()
}
