import { pb } from '../pb'
import { notifyDocumentsChanged } from '../documentEvents'

export type LibraryShare = {
  id: string
  owner: string
  recipient: string
  created: string
}

export async function listLibraryShares(owner = pb.authStore.record?.id): Promise<LibraryShare[]> {
  if (!owner) return []
  return pb.collection('library_shares').getFullList<LibraryShare>({
    filter: pb.filter('owner = {:owner}', { owner }),
    sort: 'created',
  })
}

export async function shareLibrary(recipient: string): Promise<void> {
  await pb.collection('library_shares').create({ owner: pb.authStore.record?.id, recipient })
  notifyDocumentsChanged()
}

export async function revokeLibraryShare(id: string): Promise<void> {
  await pb.collection('library_shares').delete(id)
  notifyDocumentsChanged()
}
