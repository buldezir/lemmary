import { useCallback, useEffect, useState } from 'react'
import { ClientResponseError } from 'pocketbase'
import { pb } from '../lib/pb'
import { ensureAuth } from '../lib/auth'
import {
  buildDocumentFilter,
  deleteDocuments,
  describeJobOverrides,
  markDocumentsReviewed,
  reprocessDocuments,
  searchDocuments,
  type CorrespondentRecord,
  type DocumentRecord,
  type DocumentTypeRecord,
  type JobOverrides,
} from '../lib/api/documents'
import { acceptSuggestedTag, listTags } from '../lib/api/tags'
import { getLatestJobsFor } from '../lib/api/jobs'
import {
  defaultDocumentQuery,
  documentQuerySearch,
  parseDocumentQuery,
  searchableTerm,
  tagIds,
  type DocumentQuery,
} from '../lib/documentQuery'
import { onDocumentsChanged } from '../lib/documentEvents'
import { REPROCESS_MODE_LABELS, type ProcessingJobRecord, type ReprocessMode } from '../lib/processing'
import { useAsync } from './useAsync'
import { useNavigate, useSearch } from '@tanstack/react-router'

export const DOCUMENT_PAGE_SIZE = 12

export type DocumentListRoute = '/' | '/inbox'

/**
 * Shared so the documents list and the Inbox can be two pages rather than one
 * page with an `inbox` flag threaded through its markup.
 */
export function useDocumentList({
  route,
  status: fixedStatus,
  filters = true,
  ownerOnly = false,
}: {
  route: DocumentListRoute
  /**
   * The status this list is about, when the path decides it rather than a
   * dropdown. Given one, `status` is kept out of the URL entirely, so a
   * hand-typed ?status= cannot change which list the reader is on.
   */
  status?: string
  /**
   * Whether this list offers filter controls. False pins every filter to its
   * default. Enforced here, not in the route's validateSearch: `useSearch({
   * strict: false })` hands back raw params, so a hand-typed ?q= reached the
   * query and quietly emptied a list with no search box to explain it.
   */
  filters?: boolean
  /**
   * Keeps documents other accounts shared with the caller out of this list.
   * The Inbox is one: somebody else's review queue is not yours to clear.
   */
  ownerOnly?: boolean
}) {
  // The filters are the URL, not state, so the page is reproducible and Back
  // steps through it. The URL only carries the filters that are set, so the
  // defaults are filled back in here.
  const parsed = parseDocumentQuery(useSearch({ strict: false }))
  const query = filters ? parsed : { ...defaultDocumentQuery, page: parsed.page }
  const navigate = useNavigate({ from: route })
  const {
    q: debouncedSearch,
    from: dateFrom,
    to: dateTo,
    undated,
    type: documentTypeFilter,
    correspondent: correspondentFilter,
    tags: tagFilter,
    untagged,
    page,
  } = query
  const statusFilter = fixedStatus ?? query.status
  const ownerFilter = ownerOnly ? 'mine' : query.owner

  const [documents, setDocuments] = useState<DocumentRecord[]>([])
  const [jobs, setJobs] = useState<Map<string, ProcessingJobRecord>>(new Map())
  // The only filter with a copy outside the URL: typed one letter at a time,
  // and the URL only gets the settled value.
  const [search, setSearch] = useState(debouncedSearch)
  const [syncedSearch, setSyncedSearch] = useState(debouncedSearch)
  const [totalItems, setTotalItems] = useState(0)
  const [totalPages, setTotalPages] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())
  const [reprocessMode, setReprocessMode] = useState<ReprocessMode>('auto')
  const [reprocessOverrides, setReprocessOverrides] = useState<JobOverrides>({})
  const [reprocessing, setReprocessing] = useState(false)
  const [markingReviewed, setMarkingReviewed] = useState(false)
  const [acceptingSuggestion, setAcceptingSuggestion] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [libraryVersion, setLibraryVersion] = useState(0)

  // URL -> box. Adjusted during render rather than in an effect, so the box
  // never paints one frame of the old term. syncedSearch makes this fire on a
  // URL change only, so typing is not overwritten before the debounce publishes.
  if (syncedSearch !== debouncedSearch) {
    setSyncedSearch(debouncedSearch)
    setSearch(debouncedSearch)
  }

  /**
   * A different filter is a different list, so this starts at page one unless
   * the patch says otherwise. Discrete controls push a history entry; the
   * search box replaces, so Back skips the phrase rather than one keystroke.
   */
  const updateQuery = useCallback(
    (patch: Partial<DocumentQuery>, replace = false) => {
      void navigate({
        to: route,
        search: (current) => {
          const next = documentQuerySearch({
            ...parseDocumentQuery(current),
            page: 1,
            ...patch,
          })
          if (fixedStatus !== undefined) delete next.status
          return next
        },
        replace,
      })
    },
    [navigate, route, fixedStatus],
  )

  // Box -> URL, once the typing settles. Skipped while the two already agree,
  // so the sync above cannot bounce back as a navigation.
  const term = searchableTerm(search)
  useEffect(() => {
    if (term === debouncedSearch) return
    const timer = window.setTimeout(() => updateQuery({ q: term }, true), 300)
    return () => window.clearTimeout(timer)
  }, [term, debouncedSearch, updateQuery])

  useEffect(() => {
    let active = true

    async function load(isInitial = false) {
      try {
        // Only the initial load blanks the page; realtime refreshes swap the
        // data in place instead of flashing "Loading documents...".
        if (isInitial) {
          setLoading(true)
        }
        await ensureAuth()
        const text = debouncedSearch.trim()
        const filter = buildDocumentFilter({
          status: statusFilter,
          dateFrom,
          dateTo,
          undated,
          documentType: documentTypeFilter,
          correspondent: correspondentFilter,
          tags: tagIds(tagFilter),
          untagged,
          owner: ownerFilter,
        })
        const result = text
          ? await searchDocuments({
              q: text,
              page,
              perPage: DOCUMENT_PAGE_SIZE,
              status: statusFilter,
              documentType: documentTypeFilter,
              correspondent: correspondentFilter,
              dateFrom,
              dateTo,
              undated,
              tags: tagIds(tagFilter),
              untagged,
              owner: ownerFilter,
            })
          : await pb.collection('documents').getList<DocumentRecord>(page, DOCUMENT_PAGE_SIZE, {
              sort: '-created',
              expand: 'tags,document_type,correspondent,duplicate_of',
              ...(filter ? { filter } : {}),
            })
        if (!active) return

        // Clearing a whole page leaves the URL past the end, where an empty
        // page reads as an empty list: the pager hides itself at one page, so
        // the Inbox would claim nothing was waiting while the badge counted
        // twelve.
        if (result.items.length === 0 && page > result.totalPages && result.totalItems > 0) {
          updateQuery({ page: result.totalPages }, true)
          return
        }

        setDocuments(result.items)
        setTotalItems(result.totalItems)
        setTotalPages(result.totalPages)
        setError('')
      } catch (err) {
        // Overlapping refreshes can autocancel each other; the surviving
        // request has the fresh data.
        if (err instanceof ClientResponseError && err.isAbort) {
          return
        }
        if (active) {
          setError(err instanceof Error ? err.message : 'Failed to load documents')
        }
      } finally {
        if (active && isInitial) {
          setLoading(false)
        }
      }
    }

    void load(true)

    function refresh() {
      void load()
      setLibraryVersion((version) => version + 1)
    }

    // Our own writes are the only signal that always arrives: realtime is
    // optional, and without this a marked-reviewed card would sit on an Inbox
    // whose count already said it had gone.
    const offLocal = onDocumentsChanged(refresh)

    let unsubscribe: (() => void) | undefined
    void pb
      .collection('documents')
      .subscribe('*', refresh)
      .then((fn) => {
        // Unmount can win the race with the subscribe, leaving a subscription
        // that outlives the page.
        if (!active) {
          void fn()
          return
        }
        unsubscribe = fn
      })
      .catch(() => {
        // Realtime is optional; the list is still correct as of the load.
      })

    return () => {
      active = false
      offLocal()
      unsubscribe?.()
    }
  }, [
    page,
    statusFilter,
    dateFrom,
    dateTo,
    undated,
    documentTypeFilter,
    correspondentFilter,
    tagFilter,
    untagged,
    debouncedSearch,
    ownerFilter,
    updateQuery,
  ])

  // Job changes are invisible to the documents subscription: a pipeline walks
  // six steps while the document sits at "processing", so a card's line froze
  // at "OCR" long after the badge had flipped to Failed. Debounced because a
  // bulk upload turns one answer into a burst of events.
  const [jobsVersion, setJobsVersion] = useState(0)
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined
    function schedule() {
      clearTimeout(timer)
      timer = setTimeout(() => setJobsVersion((version) => version + 1), 300)
    }

    let unsubscribe: (() => void) | undefined
    let active = true
    void pb
      .collection('processing_jobs')
      .subscribe('*', schedule)
      .then((fn) => {
        if (!active) {
          void fn()
          return
        }
        unsubscribe = fn
      })
      .catch(() => {
        // Realtime is optional; the lines are still right as of the load.
      })

    return () => {
      active = false
      clearTimeout(timer)
      void unsubscribe?.()
    }
  }, [])

  // Only failed and waiting documents need their job: it says which step broke
  // and why, or which tags the AI proposed. Every other status is
  // self-explanatory on the badge. Separate from the load above because a job
  // that cannot be read must not fail the list.
  const documentIds = documents
    .filter(
      (document) =>
        document.processing_status === 'failed' || document.processing_status === 'needs_review',
    )
    .map((document) => document.id)
    .join(',')
  useEffect(() => {
    let active = true
    // Called even for an empty page: getLatestJobsFor short-circuits without a
    // request, and this keeps the state write out of the effect body, where a
    // synchronous one would cascade a render.
    void getLatestJobsFor(documentIds ? documentIds.split(',') : [])
      .then((byDocument) => {
        if (active) setJobs(byDocument)
      })
      .catch(() => {
        // The cards fall back to the status badge alone.
      })
    return () => {
      active = false
    }
  }, [documentIds, jobsVersion])

  // Every action goes through selectedOnPage, never selectedIds, so ids left
  // over from another page or filter can neither be counted nor submitted.
  const selectedOnPage = documents.filter((document) => selectedIds.has(document.id))

  function toggleSelected(id: string) {
    setSelectedIds((current) => {
      const next = new Set(current)
      if (next.has(id)) {
        next.delete(id)
      } else {
        next.add(id)
      }
      return next
    })
  }

  async function onReprocessSelected() {
    const ids = selectedOnPage.map((document) => document.id)
    if (ids.length === 0) return

    const overrides = describeJobOverrides(reprocessOverrides)
    const confirmed = window.confirm(
      `Reprocess ${ids.length === 1 ? 'this document' : `these ${ids.length} documents`}?\n\n` +
        `Steps: ${REPROCESS_MODE_LABELS[reprocessMode]}\n` +
        (overrides ? `Models: ${overrides}\n` : '') +
        '\nExisting metadata may be overwritten.',
    )
    if (!confirmed) return

    try {
      setReprocessing(true)
      setError('')
      setMessage('')
      const result = await reprocessDocuments(ids, reprocessMode, reprocessOverrides)
      setSelectedIds(new Set())
      setMessage(
        result.skipped > 0
          ? `Queued ${result.queued}, skipped ${result.skipped} already in the queue.`
          : `Queued ${result.queued} for reprocessing.`,
      )
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Reprocess failed')
    } finally {
      setReprocessing(false)
    }
  }

  // The list reloads itself through notifyDocumentsChanged, so the accepted
  // chip turns into a real tag on the card without a manual refresh. One
  // accept at a time, and every card's chips wait for it.
  async function onAcceptSuggestedTag(id: string, name: string) {
    if (acceptingSuggestion) return
    try {
      setAcceptingSuggestion(true)
      setError('')
      setMessage('')
      await acceptSuggestedTag(id, name)
      setMessage(`Added tag "${name}".`)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not add the tag')
    } finally {
      setAcceptingSuggestion(false)
    }
  }

  /**
   * No confirmation, unlike reprocess: this overwrites nothing. Filtered to
   * documents still reading needs_review because a refresh can land between the
   * tick and the click, and the Inbox holds failed and queued documents too.
   */
  async function onMarkReviewed(ids: string[]) {
    const waiting = documents
      .filter(
        (document) => ids.includes(document.id) && document.processing_status === 'needs_review',
      )
      .map((document) => document.id)
    if (waiting.length === 0) return

    try {
      setMarkingReviewed(true)
      setError('')
      setMessage('')
      await markDocumentsReviewed(waiting)
      setSelectedIds(new Set())
      setMessage(
        waiting.length === 1 ? 'Marked reviewed.' : `Marked ${waiting.length} reviewed.`,
      )
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not mark as reviewed')
    } finally {
      setMarkingReviewed(false)
    }
  }

  /** Confirmed: the one bulk action that destroys the file, with no undo. */
  async function onDeleteSelected() {
    const ids = selectedOnPage.map((document) => document.id)
    if (ids.length === 0) return

    const confirmed = window.confirm(
      `Delete ${ids.length === 1 ? 'this document' : `these ${ids.length} documents`}?\n\n` +
        'This permanently removes the documents and their files, and cannot be undone.',
    )
    if (!confirmed) return

    try {
      setDeleting(true)
      setError('')
      setMessage('')
      await deleteDocuments(ids)
      setSelectedIds(new Set())
      setMessage(ids.length === 1 ? 'Deleted.' : `Deleted ${ids.length} documents.`)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not delete')
    } finally {
      setDeleting(false)
    }
  }

  return {
    query,
    statusFilter,
    updateQuery,
    // Undefined where the list offers no filters: on the Inbox a tag would be
    // stripped straight back out by inboxQuerySearch, so the chip stays text.
    filterByTag: filters
      ? (tagId: string) =>
          updateQuery({
            tags: [...new Set([...tagIds(tagFilter), tagId])].join(','),
            untagged: false,
          })
      : undefined,
    search,
    setSearch,
    documents,
    jobs,
    page,
    totalItems,
    totalPages,
    loading,
    error,
    message,
    libraryVersion,
    selectedIds,
    selectedOnPage,
    toggleSelected,
    selectAll: () => setSelectedIds(new Set(documents.map((document) => document.id))),
    clearSelection: () => setSelectedIds(new Set()),
    reprocessMode,
    setReprocessMode,
    reprocessOverrides,
    setReprocessOverrides,
    reprocessing,
    markingReviewed,
    deleting,
    onReprocessSelected,
    onMarkReviewed,
    onAcceptSuggestedTag,
    acceptingSuggestion,
    onDeleteSelected,
  }
}

/**
 * Its own hook rather than part of useDocumentList: a list without filter
 * controls has no use for three requests' worth of options.
 */
export function useDocumentFilterOptions() {
  const { data, error } = useAsync(async () => {
    await ensureAuth()
    const [types, correspondents, tags] = await Promise.all([
      pb.collection('document_types').getFullList<DocumentTypeRecord>({ sort: 'name' }),
      pb.collection('correspondents').getFullList<CorrespondentRecord>({ sort: 'name' }),
      listTags(),
    ])
    return { types, correspondents, tags }
  }, [])

  return {
    documentTypes: data?.types ?? [],
    correspondents: data?.correspondents ?? [],
    tags: data?.tags ?? [],
    error,
  }
}

export type DocumentList = ReturnType<typeof useDocumentList>
