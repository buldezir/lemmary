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

export async function revokeDocumentShare(shareId: string): Promise<void> {
  await pb.collection('document_shares').delete(shareId)
  notifyDocumentsChanged()
}
