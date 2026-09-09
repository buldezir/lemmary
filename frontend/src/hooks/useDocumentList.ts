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
import { getLatestJobsFor } from '../lib/api/jobs'
import {
  defaultDocumentQuery,
  documentQuerySearch,
  parseDocumentQuery,
  type DocumentQuery,
} from '../lib/documentQuery'
import { onDocumentsChanged } from '../lib/documentEvents'
import { REPROCESS_MODE_LABELS, type ProcessingJobRecord, type ReprocessMode } from '../lib/processing'
import { useAsync } from './useAsync'
import { useNavigate, useSearch } from '@tanstack/react-router'

export const DOCUMENT_PAGE_SIZE = 12

export type DocumentListRoute = '/' | '/inbox'

/**
 * Everything a page of documents needs that is not layout: the filters as URL
 * state, the load, the live refresh, the jobs behind the rows, the selection
 * and the two bulk actions.
 *
 * Extracted so the documents list and the Inbox can be two pages rather than
 * one page with an `inbox` flag threaded through its markup. They ask the same
 * questions of the server and answer them differently on screen.
 */
export function useDocumentList({
  route,
  status: fixedStatus,
  filters = true,
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
   * default, so a list without controls cannot be narrowed at all.
   *
   * Enforced here rather than left to the route's validateSearch, which is not
   * enough on its own: `useSearch({ strict: false })` hands back the raw search
   * params, so a hand-typed ?q= reached the query and quietly emptied a list
   * with no search box to explain it.
   */
  filters?: boolean
}) {
  // The filters are the URL, not state: reloading, bookmarking or sharing the
  // page reproduces the list, and Back steps through the filters that made it.
  // Validated but sparse: the URL only carries the filters that are set, so the
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
    page,
  } = query
  const statusFilter = fixedStatus ?? query.status

  const [documents, setDocuments] = useState<DocumentRecord[]>([])
  const [jobs, setJobs] = useState<Map<string, ProcessingJobRecord>>(new Map())
  // The only filter with a copy outside the URL, because it is typed one letter
  // at a time and the URL only gets the settled value.
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
  const [deleting, setDeleting] = useState(false)
  // Bumped whenever the library changes, for a caller counting it.
  const [libraryVersion, setLibraryVersion] = useState(0)

  // URL -> box, for Back/Forward and for a link opened with a term already in
  // it. Adjusted during render rather than in an effect, so the box never paints
  // one frame of the old term. syncedSearch is what makes this fire on a URL
  // change only: a keystroke leaves it alone, so the typing is not overwritten
  // before the debounce has had a chance to publish it.
  if (syncedSearch !== debouncedSearch) {
    setSyncedSearch(debouncedSearch)
    setSearch(debouncedSearch)
  }

  /**
   * Writes filters to the URL, which is what re-renders the list.
   *
   * A different filter is a different list, so it starts at page one unless the
   * patch says otherwise. Discrete controls push a history entry — Back undoes
   * the filter — while the search box replaces, so Back skips the whole phrase
   * rather than walking back one keystroke at a time.
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
  useEffect(() => {
    if (search === debouncedSearch) return
    const timer = window.setTimeout(() => updateQuery({ q: search }, true), 300)
    return () => window.clearTimeout(timer)
  }, [search, debouncedSearch, updateQuery])

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
            })
          : await pb.collection('documents').getList<DocumentRecord>(page, DOCUMENT_PAGE_SIZE, {
              sort: '-created',
              expand: 'tags,document_type,correspondent,duplicate_of',
              ...(filter ? { filter } : {}),
            })
        if (!active) return

        // Clearing a whole page leaves the URL past the end, where an empty
        // page reads as an empty *list*: the pager hides itself at one page, so
        // the Inbox would claim nothing was waiting while the badge counted
        // twelve. Walk back instead of reporting it.
        if (result.items.length === 0 && page > result.totalPages && result.totalItems > 0) {
          updateQuery({ page: result.totalPages }, true)
          return
        }

        setDocuments(result.items)
        setTotalItems(result.totalItems)
        setTotalPages(result.totalPages)
        setError('')
      } catch (err) {
        // Overlapping refreshes (filter change + realtime) can autocancel each
        // other; the surviving request has the fresh data.
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

    // Our own writes, which is the only signal that always arrives: realtime is
    // optional, and without this a marked-reviewed card would sit on an Inbox
    // it no longer belongs to while the header's count already said it had gone.
    const offLocal = onDocumentsChanged(refresh)

    let unsubscribe: (() => void) | undefined
    void pb
      .collection('documents')
      .subscribe('*', refresh)
      .then((fn) => {
        // Unmount can win the race with the subscribe; without this the
        // subscription outlives the page that asked for it.
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
    debouncedSearch,
    updateQuery,
  ])

  // Bumped when a job changes, which the documents subscription cannot see: a
  // pipeline walks through six steps while the document sits at "processing",
  // so without this a card's line froze at "OCR — 12.0s" and stayed there long
  // after the badge had flipped to Failed. Debounced because a bulk upload
  // turns one answer into a burst of events.
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

  // The jobs behind the page's documents, in one request, so a card can say
  // which step is running or why one failed. Separate from the load above
  // because it depends only on which documents ended up on the page -- and
  // because a job that cannot be read must not fail the list itself.
  const documentIds = documents.map((document) => document.id).join(',')
  useEffect(() => {
    let active = true
    // Called even for an empty page: getLatestJobsFor short-circuits without a
    // request, and going through it keeps the state write out of the effect
    // body, where a synchronous one would cascade a render.
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

  // Every action goes through selectedOnPage, never selectedIds, so ids left over
  // from another page or an earlier filter can neither be counted nor submitted.
  // That is what makes a stale selection harmless without resetting state on
  // every filter change.
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

  /**
   * Clears documents out of the Inbox. No confirmation, unlike reprocess: this
   * overwrites nothing.
   *
   * Filtered to documents still reading needs_review, on top of the
   * selectedOnPage guard, because a refresh can land between the tick and the
   * click -- and because the Inbox holds failed and queued documents too, which
   * there is nothing to review about.
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

  /**
   * Deletes what is selected. Confirmed, unlike marking reviewed: this is the
   * one bulk action that destroys the document and its file, and there is no
   * undo behind it.
   */
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
    onDeleteSelected,
  }
}

/**
 * The document types and correspondents a filter dropdown offers. Its own hook
 * rather than part of useDocumentList: a list without filter controls has no
 * use for two requests' worth of options.
 */
export function useDocumentFilterOptions() {
  const { data, error } = useAsync(async () => {
    await ensureAuth()
    const [types, correspondents] = await Promise.all([
      pb.collection('document_types').getFullList<DocumentTypeRecord>({ sort: 'name' }),
      pb.collection('correspondents').getFullList<CorrespondentRecord>({ sort: 'name' }),
    ])
    return { types, correspondents }
  }, [])

  return {
    documentTypes: data?.types ?? [],
    correspondents: data?.correspondents ?? [],
    error,
  }
}

export type DocumentList = ReturnType<typeof useDocumentList>
