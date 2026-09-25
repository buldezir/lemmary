import { pb } from '../pb'
import { listUsers, type UserSummary } from './users'
import { notifyDocumentsChanged } from '../documentEvents'

export type ShareRecord = {
  id: string
  document: string
  user: string
  created: string
}

export type DirectoryUser = UserSummary

export async function listShareRecipients(): Promise<DirectoryUser[]> {
  const self = pb.authStore.record?.id
  const users = await listUsers()
  return users
    .filter((user) => user.id !== self)
    .sort((a, b) => a.email.localeCompare(b.email))
}

/**
 * No expand on the recipient: the users collection stays owner-only, so an
 * owner cannot read the account they shared with and PocketBase would drop the
 * expansion without saying so, leaving every row reading "Unknown account".
 * Names come from the directory instead.
 */
export async function listDocumentShares(documentId: string): Promise<ShareRecord[]> {
  return pb.collection('document_shares').getFullList<ShareRecord>({
    filter: pb.filter('document = {:documentId}', { documentId }),
    sort: 'created',
  })
}

export async function shareDocument(documentId: string, userId: string): Promise<void> {
  await pb.collection('document_shares').create({ document: documentId, user: userId })
  notifyDocumentsChanged()
}

/** Skips documents already shared with the account, so a repeat is not a failure. */
export async function shareDocuments(documentIds: string[], userId: string): Promise<number> {
  if (documentIds.length === 0) return 0
  const existing = await pb.collection('document_shares').getFullList<ShareRecord>({
    filter: pb.filter(
      `user = {:userId} && (${documentIds.map((_, i) => `document = {:d${i}}`).join(' || ')})`,
      Object.fromEntries([['userId', userId], ...documentIds.map((id, i) => [`d${i}`, id])]),
    ),
  })
  const already = new Set(existing.map((share) => share.document))
  const pending = documentIds.filter((id) => !already.has(id))

  const results = await Promise.allSettled(
    pending.map((document) =>
      pb.collection('document_shares').create({ document, user: userId }, { requestKey: null }),
    ),
  )
  notifyDocumentsChanged()

  const failed = results.filter((result) => result.status === 'rejected').length
  if (failed > 0) {
    throw new Error(
      failed === pending.length ? 'Could not share.' : `Shared ${pending.length - failed}; ${failed} failed.`,
    )
  }
  return pending.length
}

export async function revokeDocumentShare(shareId: string): Promise<void> {
  await pb.collection('document_shares').delete(shareId)
  notifyDocumentsChanged()
}
