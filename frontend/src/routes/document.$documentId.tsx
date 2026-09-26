import { type SubmitEvent, useEffect, useRef, useState } from 'react'
import { Link, useNavigate, useParams } from '@tanstack/react-router'
import { ClientResponseError } from 'pocketbase'
import { pb } from '../lib/pb'
import { ensureAuth } from '../lib/auth'
import {
  describeJobOverrides,
  markDocumentsReviewed,
  openDocumentFile,
  overridesForSteps,
  reprocessDocument,
  saveDocumentMetadata,
  translateOcrText,
  type DocumentRecord,
  type JobOverrides,
} from '../lib/api/documents'
import { acceptSuggestedTag, listTags, type TagRecord } from '../lib/api/tags'
import { pendingTagSuggestions } from '../lib/tagSuggestions'
import { SuggestedTags } from '../components/SuggestedTags'
import { StepBindingOverride } from '../components/BindingOverride'
import { Combobox } from '../components/Combobox'
import { useAsync } from '../hooks/useAsync'
import { DOCUMENT_STATUS_LABELS } from '../lib/documentStatus'
import {
  defaultReprocessSteps,
  forceStepsForReprocess,
  FULL_PIPELINE_STEPS,
  orderedProcessingSteps,
  PROCESSING_STEP_DESCRIPTIONS,
  PROCESSING_STEP_LABELS,
  formatDuration,
  jobDurationMs,
  jobStillRunning,
  type ProcessingJobRecord,
  type ProcessingStep,
  summarizeJob,
} from '../lib/processing'
import { ProcessingStatus } from '../components/ProcessingStatus'
import { ProcessingSteps } from '../components/ProcessingSteps'
import { Button } from '../components/ui'
import { ShareDialog, ShareSummary } from '../components/ShareDialog'
import { DocumentPreview } from '../components/DocumentPreview'
import { useStoredFlag } from '../hooks/useStoredFlag'
import { useAppMeta } from '../hooks/useAppMeta'
import { previewKind } from '../lib/documentPreview'

type OcrView = 'original' | 'translated' | 'both'

const OCR_VIEW_LABELS: Record<OcrView, string> = {
  original: 'Original',
  translated: 'Translated',
  both: 'Side by side',
}

/**
 * Only a document still waiting for its owner's review came from the Inbox; a
 * shared one is never in it, because the Inbox lists only the caller's own.
 */
function backTarget(document: DocumentRecord | null, owned: boolean): '/' | '/inbox' {
  return owned && document?.processing_status === 'needs_review' ? '/inbox' : '/'
}

function backLabel(to: '/' | '/inbox') {
  return to === '/inbox' ? 'Back to the Inbox' : 'Back to Documents'
}

export function DocumentDetailPage() {
  const { documentId } = useParams({ from: '/document/$documentId' })
  const navigate = useNavigate()
  const [document, setDocument] = useState<DocumentRecord | null>(null)
  const [job, setJob] = useState<ProcessingJobRecord | null>(null)
  const [tagIds, setTagIds] = useState<string[]>([])
  // The whole vocabulary, loaded once: the picker needs every tag, not only the
  // ones this document carries.
  const { data: vocabulary, error: vocabularyError } = useAsync(listTags, [])
  const [documentTypeInput, setDocumentTypeInput] = useState('')
  const [correspondentInput, setCorrespondentInput] = useState('')
  const [loading, setLoading] = useState(true)
  const [editing, setEditing] = useState(false)
  const [saving, setSaving] = useState(false)
  const [markingReviewed, setMarkingReviewed] = useState(false)
  const [acceptingSuggestion, setAcceptingSuggestion] = useState(false)
  const [reprocessing, setReprocessing] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [reprocessSteps, setReprocessSteps] = useState<ProcessingStep[]>([])
  const [reprocessOverrides, setReprocessOverrides] = useState<JobOverrides>({})
  // null means "the reader has not said", which lets the panel open itself for
  // a failed job. Once they toggle it, their choice is a boolean and sticks.
  const [showProcessingJob, setShowProcessingJob] = useState<boolean | null>(null)
  const [showPreview, setShowPreview] = useStoredFlag('lemmary.showPreview', true)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')

  // Mirrors `editing` for the load callback below: a background refresh must
  // not clobber the form while the user is typing.
  const editingRef = useRef(editing)
  useEffect(() => {
    editingRef.current = editing
  }, [editing])

  // The last server record the form was filled from, kept even while editing so
  // locking the form again can drop the unsaved edits it is hiding.
  const loadedRef = useRef<DocumentRecord | null>(null)

  // Which (document, has-OCR-text) pair the reprocess defaults were computed
  // for, so background refreshes do not reset the user's selection.
  const reprocessDefaultsKey = useRef('')

  function applyLoadedDocument(doc: DocumentRecord) {
    loadedRef.current = doc
    setDocument(doc)
    setTagIds(doc.tags ?? [])
    setDocumentTypeInput(doc.expand?.document_type?.name ?? '')
    setCorrespondentInput(doc.expand?.correspondent?.name ?? '')

    const hasOcr = Boolean(doc.ocr_text?.trim())
    const key = `${doc.id}:${hasOcr}`
    if (reprocessDefaultsKey.current !== key) {
      reprocessDefaultsKey.current = key
      setReprocessSteps(defaultReprocessSteps(hasOcr))
    }
  }

  useEffect(() => {
    let active = true
    let unsubscribe: (() => void) | undefined
    let poll: ReturnType<typeof setInterval> | undefined

    async function load(isInitial = false) {
      try {
        if (isInitial) {
          setLoading(true)
        }
        await ensureAuth()

        const doc = await pb.collection('documents').getOne<DocumentRecord>(documentId, {
          expand: 'tags,document_type,correspondent,duplicate_of',
        })

        const jobs = await pb.collection('processing_jobs').getList<ProcessingJobRecord>(1, 1, {
          filter: pb.filter('document = {:documentId}', { documentId }),
          sort: '-created',
        })

        if (!active) {
          return
        }

        setJob(jobs.items[0] ?? null)
        loadedRef.current = doc
        if (!editingRef.current) {
          applyLoadedDocument(doc)
        }
        setError('')

        // The job, not just the document: apply_metadata marks the document
        // completed before embed runs, so watching the document alone stops
        // the poll mid-pipeline with embed stuck reading 'running'.
        const inFlight =
          doc.processing_status === 'processing' ||
          doc.processing_status === 'pending' ||
          jobStillRunning(jobs.items[0])
        if (inFlight && poll == null) {
          poll = setInterval(() => {
            void load()
          }, 1000)
        }
        if (!inFlight && poll != null) {
          clearInterval(poll)
          poll = undefined
        }
      } catch (err) {
        // Overlapping refreshes (poll + realtime) can autocancel each other;
        // the surviving request has the fresh data, so that is not an error.
        if (err instanceof ClientResponseError && err.isAbort) {
          return
        }
        if (active) {
          setError(err instanceof Error ? err.message : 'Failed to load document')
        }
      } finally {
        if (active && isInitial) {
          setLoading(false)
        }
      }
    }

    void load(true)

    void pb
      .collection('documents')
      .subscribe(documentId, (event) => {
        if (event.action === 'delete') {
          return
        }
        void load()
      })
      .then((fn) => {
        unsubscribe = fn
      })
      .catch(() => {
        // Realtime is optional; polling still refreshes processing status.
      })

    return () => {
      active = false
      unsubscribe?.()
      if (poll != null) {
        clearInterval(poll)
      }
    }
  }, [documentId])

  const hasOcrText = Boolean(document?.ocr_text?.trim())
  // A document another account shared is read-only: the rules refuse every
  // write, so offering the controls would only produce 403s.
  const owned = Boolean(document) && document?.user === pb.authStore.record?.id
  const [sharing, setSharing] = useState(false)
  const [shareVersion, setShareVersion] = useState(0)
  const back = backTarget(document, owned)

  const { resultLanguage } = useAppMeta()
  const canTranslate = Boolean(resultLanguage) && hasOcrText && !editing
  const [ocrView, setOcrView] = useState<OcrView>('original')
  const translationWanted = canTranslate && ocrView !== 'original'
  const ocrText = document?.ocr_text ?? ''
  const forceTranslation = useRef(false)
  const translation = useAsync(() => {
    if (!translationWanted) {
      return Promise.resolve(null)
    }
    const force = forceTranslation.current
    forceTranslation.current = false
    return translateOcrText(documentId, force)
  }, [translationWanted, documentId, ocrText])
  const [retranslating, setRetranslating] = useState(false)
  const translating = translationWanted && (translation.loading || retranslating)
  const sideBySide = translationWanted && ocrView === 'both'
  const ocrTextareaClass = `min-h-[28.8rem] font-mono text-xs leading-relaxed ${sideBySide ? 'sm:h-[57.6rem]' : ''}`

  const originalOcrRef = useRef<HTMLTextAreaElement>(null)
  const translatedOcrRef = useRef<HTMLTextAreaElement>(null)
  // Setting the other pane's scrollTop fires its scroll event in turn; that
  // echo is skipped so the two panes do not fight over the position.
  const scrollEcho = useRef<HTMLTextAreaElement | null>(null)
  function syncOcrScroll(from: HTMLTextAreaElement | null, to: HTMLTextAreaElement | null) {
    if (!sideBySide || !from || !to) {
      return
    }
    if (scrollEcho.current === from) {
      scrollEcho.current = null
      return
    }
    const ratio = from.scrollTop / Math.max(1, from.scrollHeight - from.clientHeight)
    const target = Math.round(ratio * (to.scrollHeight - to.clientHeight))
    if (target !== to.scrollTop) {
      scrollEcho.current = to
      to.scrollTop = target
    }
  }

  async function onRetranslate() {
    forceTranslation.current = true
    setRetranslating(true)
    await translation.reload()
    setRetranslating(false)
  }

  // The pane only fits beside the fields from xl up, and iOS Safari and Android
  // Chrome do not render a framed PDF at all. Gated in JS rather than by CSS so
  // a phone does not download a file it will never show.
  const [wideEnough, setWideEnough] = useState(() => previewViewport().matches)
  useEffect(() => {
    const query = previewViewport()
    const onChange = (event: MediaQueryListEvent) => setWideEnough(event.matches)
    query.addEventListener('change', onChange)
    return () => query.removeEventListener('change', onChange)
  }, [])

  // docx, xlsx and the plain-text types have nothing a browser can frame, so
  // they get no pane and no toggle and keep the full width for their fields.
  const canPreview =
    wideEnough && Boolean(document?.file) && previewKind(document?.file ?? '') !== 'none'

  // Latches on the first show: hiding the pane must not unmount the viewer,
  // because a remount resets a PDF to page one and loses find-in-document.
  // Not mounted before that, so a hidden pane downloads nothing.
  const [previewMounted, setPreviewMounted] = useState(showPreview)

  // The job as well as the document, for the reason the poll gate above gives:
  // trusting the document alone re-enables this form mid-pipeline and invites
  // a second job over a document the first one is still writing.
  const canReprocess =
    owned &&
    document?.processing_status !== 'processing' &&
    document?.processing_status !== 'pending' &&
    !jobStillRunning(job)

  // A clock for the durations still counting. In state rather than read during
  // render, which would be an impure read that advanced only when something
  // else re-rendered. Separate from the document poll, so the elapsed keeps
  // time even if a refresh is slow.
  const [tick, setTick] = useState(() => Date.now())
  const stepRunning = (job?.step_runs ?? []).some((run) => run.status === 'running')
  const needsClock = jobStillRunning(job) || stepRunning
  useEffect(() => {
    if (!needsClock) return
    const id = setInterval(() => setTick(Date.now()), 1000)
    return () => clearInterval(id)
  }, [needsClock])

  const jobTotalMs = job ? jobDurationMs(job, tick) : null
  const summary = summarizeJob(job, tick)
  // Latched, not derived: a panel that opened itself to show a failure must not
  // close again the moment Reprocess turns the tone back to 'running'. Warnings
  // count too, since a soft-failed embed is the one failure the status badge
  // never mentions.
  const [autoOpened, setAutoOpened] = useState(false)
  // Reset per document: the route param can change without this component
  // remounting, and a panel closed on one document must not hide the next
  // document's failure. The OCR view resets in the same render, before the
  // translation loader's effect can commit, so opening a document never starts
  // a translation.
  const [panelDocumentId, setPanelDocumentId] = useState(documentId)
  if (panelDocumentId !== documentId) {
    setPanelDocumentId(documentId)
    setShowProcessingJob(null)
    setAutoOpened(false)
    setOcrView('original')
  }
  if (!autoOpened && (summary?.tone === 'error' || summary?.tone === 'warning')) {
    setAutoOpened(true)
  }
  const jobPanelOpen = owned && (showProcessingJob ?? autoOpened)

  function toggleReprocessStep(step: ProcessingStep) {
    setReprocessSteps((current) => {
      if (current.includes(step)) {
        return current.filter((name) => name !== step)
      }
      return orderedProcessingSteps([...current, step])
    })
  }

  function canSelectReprocessStep(step: ProcessingStep): boolean {
    if (!canReprocess) {
      return false
    }
    if (step === 'extract_metadata' || step === 'detect_duplicates') {
      return hasOcrText || reprocessSteps.includes('ocr')
    }
    return true
  }

  function onReprocessSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!document || !canReprocess || reprocessSteps.length === 0) {
      return
    }
    if (
      reprocessSteps.includes('extract_metadata') &&
      !hasOcrText &&
      !reprocessSteps.includes('ocr')
    ) {
      setError('Extract metadata requires OCR text. Select OCR or run OCR first.')
      return
    }

    const stepLabels = reprocessSteps.map((step) => PROCESSING_STEP_LABELS[step]).join(', ')
    // The same narrowing reprocessDocument does, so a step that was unticked
    // does not name a model in the confirmation.
    const overrides = describeJobOverrides(overridesForSteps(reprocessOverrides, reprocessSteps))
    const confirmed = window.confirm(
      `Re-run these steps?\n\n${stepLabels}\n` +
        (overrides ? `\nModels: ${overrides}\n` : '') +
        '\nExisting metadata may be overwritten.',
    )
    if (confirmed) {
      void onReprocess()
    }
  }

  async function onReprocess() {
    if (!document || !canReprocess || reprocessSteps.length === 0) {
      return
    }

    try {
      setReprocessing(true)
      setMessage('')
      setError('')

      const steps = orderedProcessingSteps(reprocessSteps)
      await reprocessDocument(
        document.id,
        steps,
        forceStepsForReprocess(steps),
        reprocessOverrides,
      )

      // Confirmed as soon as the job exists: queueing wakes the realtime
      // subscription, whose load() autocancels the two requests below, and the
      // confirmation is owed either way.
      setMessage(
        `Document queued for reprocessing (${steps.map((step) => PROCESSING_STEP_LABELS[step]).join(', ')}).`,
      )

      const doc = await pb.collection('documents').getOne<DocumentRecord>(document.id, {
        expand: 'tags,document_type,correspondent,duplicate_of',
      })
      const jobs = await pb.collection('processing_jobs').getList<ProcessingJobRecord>(1, 1, {
        filter: pb.filter('document = {:documentId}', { documentId: document.id }),
        sort: '-created',
      })

      applyLoadedDocument(doc)
      setJob(jobs.items[0] ?? null)
    } catch (err) {
      // An autocancel means the refresh above lost a race it did not need to
      // win: the surviving load() has the fresh document and the new job.
      if (err instanceof ClientResponseError && err.isAbort) {
        return
      }
      setError(err instanceof Error ? err.message : 'Failed to reprocess document')
    } finally {
      setReprocessing(false)
    }
  }

  async function onDelete() {
    if (!document || deleting) {
      return
    }

    const title = document.title?.trim() || 'Untitled document'
    const confirmed = window.confirm(
      `Delete "${title}"?\n\nThis permanently removes the document and cannot be undone.`,
    )
    if (!confirmed) {
      return
    }

    try {
      setDeleting(true)
      setMessage('')
      setError('')
      await pb.collection('documents').delete(document.id)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete document')
      setDeleting(false)
      return
    }

    await navigate({ to: back })
  }

  /**
   * Hidden while editing, because Save *is* this action in edit mode:
   * saveDocumentMetadata already turns needs_review into completed.
   */
  async function onMarkReviewed() {
    if (!document) return

    try {
      setMarkingReviewed(true)
      setMessage('')
      setError('')
      await markDocumentsReviewed([document.id])
      const refreshed = await pb.collection('documents').getOne<DocumentRecord>(document.id, {
        expand: 'tags,document_type,correspondent,duplicate_of',
      })
      applyLoadedDocument(refreshed)
      setMessage('Marked as reviewed.')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not mark as reviewed')
    } finally {
      setMarkingReviewed(false)
    }
  }

  // The chips hide while editing, but a click can still be in flight when the
  // form unlocks. Then, like load(), the server copy must not replace the
  // half-edited form; only the picker's selection and its known tags learn
  // about the new one, so the chip has a name and the eventual Save does not
  // write the pre-accept list back over it.
  async function onAcceptSuggestedTag(name: string) {
    if (!document || acceptingSuggestion) return
    try {
      setAcceptingSuggestion(true)
      setMessage('')
      setError('')
      const tag = await acceptSuggestedTag(document.id, name)
      // requestKey null, as in onSave: the PATCH wakes the realtime load().
      const refreshed = await pb.collection('documents').getOne<DocumentRecord>(document.id, {
        expand: 'tags,document_type,correspondent,duplicate_of',
        requestKey: null,
      })
      if (editingRef.current) {
        loadedRef.current = refreshed
        setDocument((current) =>
          current && { ...current, expand: { ...current.expand, tags: refreshed.expand?.tags ?? [] } },
        )
        setTagIds((current) => (current.includes(tag.id) ? current : [...current, tag.id]))
      } else {
        applyLoadedDocument(refreshed)
      }
      setMessage(`Added tag "${name}".`)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not add the tag')
    } finally {
      setAcceptingSuggestion(false)
    }
  }

  function toggleEditing() {
    // editingRef alongside the state: a load() already in flight reads the ref,
    // and the effect that mirrors it does not run until after this render.
    if (!editing) {
      setEditing(true)
      editingRef.current = true
      return
    }
    setEditing(false)
    editingRef.current = false
    if (loadedRef.current) {
      applyLoadedDocument(loadedRef.current)
    }
  }

  async function onSave(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!document || !editing) {
      return
    }

    try {
      setSaving(true)
      setMessage('')
      setError('')

      await saveDocumentMetadata(document.id, {
        title: document.title,
        purpose: document.purpose,
        summary: document.summary,
        ocrText: document.ocr_text ?? '',
        documentDate: document.document_date,
        documentTypeName: documentTypeInput,
        correspondentName: correspondentInput,
        tagIds,
        processingStatus: document.processing_status,
      })

      setEditing(false)
      editingRef.current = false
      // requestKey null: the PATCH wakes the realtime subscription, whose
      // load() would otherwise autocancel this refresh and the confirmation
      // with it, then wipe the abort error, leaving no word that it saved.
      const refreshed = await pb.collection('documents').getOne<DocumentRecord>(document.id, {
        expand: 'tags,document_type,correspondent,duplicate_of',
        requestKey: null,
      })
      applyLoadedDocument(refreshed)
      setMessage('Metadata saved.')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save metadata')
    } finally {
      setSaving(false)
    }
  }

  if (loading) {
    return <p className="text-sm text-ink-soft">Loading document...</p>
  }

  if (!document) {
    return (
      <section className="flex flex-col gap-3">
        <p className="text-sm text-madder">{error || 'Document not found.'}</p>
        <Link to={back} className="text-sm font-medium text-oxblood underline">
          {backLabel(back)}
        </Link>
      </section>
    )
  }

  return (
    <section className="flex flex-col gap-5">
      {document.duplicate_of && (
        <div className="rounded-xs border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-900">
          Possible duplicate of{' '}
          <Link
            to="/document/$documentId"
            params={{ documentId: document.duplicate_of }}
            className="font-medium underline"
          >
            {document.expand?.duplicate_of?.title?.trim() || document.duplicate_of}
          </Link>
          .{' '}
          {/* The relationship stays true after review, so the banner stays,
              but stops asking for something already done. */}
          {document.processing_status === 'needs_review'
            ? 'Review both documents and delete the one you do not need.'
            : 'Reviewed; both were kept.'}
        </div>
      )}
      <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
        <div>
          <Link to={back} className="text-sm text-ink-soft hover:text-oxblood">
            &larr; {backLabel(back)}
          </Link>
          <h2 className="mt-1 font-display text-2xl font-semibold tracking-tight text-ink">
            {document.title || 'Untitled document'}
          </h2>
          <p className="text-sm text-ink-soft">
            Status: {DOCUMENT_STATUS_LABELS[document.processing_status]}
          </p>
          <ShareSummary
            documentId={documentId}
            ownerId={document.user}
            owned={owned}
            version={shareVersion}
          />
          <ProcessingStatus summary={summary} />
        </div>
        <div className="flex flex-wrap items-center gap-2 lg:shrink-0 lg:justify-end">
          {canPreview && (
            <button
              type="button"
              aria-pressed={showPreview}
              onClick={() => {
                setPreviewMounted(true)
                setShowPreview((visible) => !visible)
              }}
              className={`rounded-xs border px-4 py-2 text-sm font-medium transition-colors ${
                showPreview
                  ? 'border-ink bg-ink text-paper hover:bg-oxblood'
                  : 'border-line-strong bg-surface text-ink-muted hover:bg-bright'
              }`}
            >
              Preview
            </button>
          )}
          {document.file && (
            <button
              type="button"
              className="rounded-xs border border-line-strong bg-surface px-4 py-2 text-sm font-medium text-ink-muted transition-colors hover:bg-bright"
              onClick={() => void openDocumentFile(document)}
            >
              Open file
            </button>
          )}
          {/* Always offered to the owner, job or no job: a document restored from
              an export has no job record, and hiding the panel leaves it with no
              way to be reprocessed. A reader cannot list the owner's jobs. */}
          {owned && (
          <button
            type="button"
            onClick={() => setShowProcessingJob(!jobPanelOpen)}
            aria-label={
              jobPanelOpen ? 'Hide processing job details' : 'Show processing job details'
            }
            aria-pressed={jobPanelOpen}
            title={jobPanelOpen ? 'Hide processing job' : 'Show processing job'}
            className={`flex shrink-0 items-center gap-1.5 rounded-xs border px-4 py-2 text-sm font-medium transition-colors ${
              jobPanelOpen
                ? 'border-ink bg-ink text-paper hover:bg-oxblood'
                : 'border-line-strong bg-surface text-ink-muted hover:bg-bright hover:text-ink'
            }`}
          >
            <svg
              xmlns="http://www.w3.org/2000/svg"
              viewBox="0 0 100 100"
              fill="currentColor"
              className="h-4 w-4"
              aria-hidden="true"
            >
              <path d="M82.17,82.17a2.49,2.49,0,0,1-.32.26h6a2.5,2.5,0,0,1,0,5h-12a2.5,2.5,0,0,1-2.5-2.5v-12a2.5,2.5,0,0,1,5,0v6l.26-.28A40.5,40.5,0,0,0,50,9.5a2.5,2.5,0,0,1,0-5A45.5,45.5,0,0,1,82.17,82.17ZM4.5,50A45.5,45.5,0,0,0,50,95.5a2.5,2.5,0,0,0,0-5A40.5,40.5,0,0,1,21.36,21.36c.08-.08.17-.18.26-.29v6a2.5,2.5,0,0,0,5,0v-12a2.5,2.5,0,0,0-2.5-2.5h-12a2.5,2.5,0,0,0,0,5h6a3.72,3.72,0,0,0-.31.26A45.2,45.2,0,0,0,4.5,50ZM58.63,41.37a12.2,12.2,0,1,1-17.25,0A12.21,12.21,0,0,1,58.63,41.37Zm-3.54,3.54a7.2,7.2,0,1,0,0,10.18A7.21,7.21,0,0,0,55.09,44.91ZM67.3,67.31a24.68,24.68,0,0,1-2.59,2.25L65,72.84a6.47,6.47,0,0,1-4.78,6.83L56,80.81a6.48,6.48,0,0,1-7.56-3.53l-1.39-3a24.24,24.24,0,0,1-6.61-1.77l-2.7,1.89a6.48,6.48,0,0,1-8.31-.72l-3.11-3.11a6.47,6.47,0,0,1-.72-8.31l1.89-2.7A24.24,24.24,0,0,1,25.71,53l-3-1.39A6.48,6.48,0,0,1,19.19,44l1.14-4.25A6.46,6.46,0,0,1,27.16,35l3.28.29a24.72,24.72,0,0,1,4.84-4.84L35,27.16a6.48,6.48,0,0,1,4.78-6.83L44,19.19a6.48,6.48,0,0,1,7.56,3.53l1.39,3a24.24,24.24,0,0,1,6.61,1.77l2.7-1.89a6.47,6.47,0,0,1,8.31.72l3.11,3.11a6.48,6.48,0,0,1,.72,8.31l-1.89,2.7A24.21,24.21,0,0,1,74.29,47l3,1.39A6.48,6.48,0,0,1,80.81,56l-1.14,4.25A6.46,6.46,0,0,1,72.84,65l-3.28-.29A24.69,24.69,0,0,1,67.3,67.31Zm1.26-7.7,4.71.42a1.48,1.48,0,0,0,1.56-1.09L76,54.69A1.48,1.48,0,0,0,75.17,53l-4.3-2a2.5,2.5,0,0,1-1.44-2.12,19.31,19.31,0,0,0-2-7.55,2.5,2.5,0,0,1,.19-2.56l2.72-3.88a1.48,1.48,0,0,0-.17-1.9L67,29.84a1.48,1.48,0,0,0-1.9-.17L61.27,32.4a2.5,2.5,0,0,1-2.55.19,19.33,19.33,0,0,0-7.55-2A2.5,2.5,0,0,1,49,29.12l-2-4.3A1.48,1.48,0,0,0,45.31,24l-4.25,1.14A1.48,1.48,0,0,0,40,26.72l.42,4.71a2.5,2.5,0,0,1-1.11,2.31,19.54,19.54,0,0,0-3,2.49h0a19.51,19.51,0,0,0-2.49,3,2.5,2.5,0,0,1-2.31,1.11L26.72,40a1.48,1.48,0,0,0-1.56,1.09L24,45.31A1.48,1.48,0,0,0,24.83,47l4.3,2a2.5,2.5,0,0,1,1.44,2.12,19.33,19.33,0,0,0,2,7.55,2.5,2.5,0,0,1-.19,2.55l-2.72,3.88a1.48,1.48,0,0,0,.17,1.9L33,70.16a1.48,1.48,0,0,0,1.9.17l3.88-2.72a2.5,2.5,0,0,1,2.55-.19,19.33,19.33,0,0,0,7.55,2A2.5,2.5,0,0,1,51,70.88l2,4.3a1.48,1.48,0,0,0,1.73.81l4.25-1.14A1.48,1.48,0,0,0,60,73.28l-.42-4.71a2.5,2.5,0,0,1,1.11-2.31,19.67,19.67,0,0,0,5.54-5.54A2.5,2.5,0,0,1,68.57,59.61Z" />
            </svg>
            Job
          </button>
          )}
          {owned && (
          <button
            type="button"
            onClick={toggleEditing}
            aria-pressed={editing}
            aria-label={editing ? 'Lock metadata editing' : 'Unlock metadata editing'}
            title={editing ? 'Lock editing and discard unsaved changes' : 'Unlock editing'}
            className={`flex shrink-0 items-center justify-center rounded-xs border px-2.5 py-2 transition-colors ${
              editing
                ? 'border-ink bg-ink text-paper hover:bg-oxblood'
                : 'border-line-strong bg-surface text-ink-muted hover:bg-bright hover:text-ink'
            }`}
          >
            <svg
              xmlns="http://www.w3.org/2000/svg"
              viewBox="0 0 20 20"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.5"
              strokeLinecap="round"
              strokeLinejoin="round"
              className="h-5 w-5"
              aria-hidden="true"
            >
              <rect x="4.5" y="9" width="11" height="8" rx="1.5" />
              {/* The shackle's right leg is what comes off the body when the
                  form is open for editing. */}
              <path d={editing ? 'M7 9V6a3 3 0 0 1 6 0' : 'M7 9V6a3 3 0 0 1 6 0v3'} />
            </svg>
          </button>
          )}
          {owned && document.processing_status === 'needs_review' && (
            <Button
              variant="secondary"
              disabled={markingReviewed || editing}
              title={editing ? 'Lock editing first' : undefined}
              onClick={() => void onMarkReviewed()}
            >
              {markingReviewed ? 'Marking...' : 'Mark reviewed'}
            </Button>
          )}
          {owned && (
            <Button variant="secondary" onClick={() => setSharing(true)}>
              Share
            </Button>
          )}
          <Link
            to="/document/$documentId/ask"
            params={{ documentId }}
            aria-disabled={!hasOcrText}
            title={
              hasOcrText ? 'Ask questions about this document' : 'OCR text required before asking AI'
            }
            className={`rounded-xs border px-4 py-2 text-sm font-medium transition-colors ${
              hasOcrText
                ? 'border-ink bg-ink text-paper hover:bg-oxblood'
                : 'pointer-events-none border-line bg-paper text-ink-faint'
            }`}
          >
            Ask AI
          </Link>
          {owned && (
            <div className="ml-1 border-l border-line pl-3">
              <Button variant="danger" onClick={() => void onDelete()} disabled={deleting}>
                {deleting ? 'Deleting...' : 'Delete'}
              </Button>
            </div>
          )}
        </div>
      </div>

      <ShareDialog
        documentId={documentId}
        open={sharing}
        onClose={() => {
          setSharing(false)
          setShareVersion((version) => version + 1)
        }}
      />

      {/* The pane sits left of the form but after it in the DOM, so a keyboard
          reaches the fields without tabbing through the PDF viewer first. */}
      <div className="flex flex-col gap-5 xl:flex-row xl:items-start">
        <div className="flex min-w-0 flex-1 flex-col gap-5">
          {jobPanelOpen && (
            <div className="rounded-none border border-line bg-surface p-3">
              {job ? (
                <>
                  <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                    <h3 className="text-sm font-semibold text-ink">Processing job</h3>
                    <span className="bg-wash px-1.5 py-0.5 text-xs font-medium text-ink-muted">
                      {job.status}
                    </span>
                    {jobTotalMs !== null ? (
                      <span className="text-xs text-ink-soft">total: {formatDuration(jobTotalMs)}</span>
                    ) : null}
                  </div>
                  <div className="mt-2">
                    <ProcessingSteps job={job} now={tick} />
                  </div>
                </>
              ) : (
                <div className="flex flex-col gap-1">
                  <h3 className="text-sm font-semibold text-ink">Processing job</h3>
                  <p className="text-xs text-ink-soft">
                    No processing job is recorded for this document. It was imported from an export or
                    added before the pipeline kept a history. Reprocessing it below creates one and
                    runs the steps you choose.
                  </p>
                </div>
              )}

              <form
                className="mt-3 flex flex-col gap-2 border-t border-line pt-3"
                onSubmit={onReprocessSubmit}
              >
                <div>
                  <h4 className="text-sm font-semibold text-ink">Reprocess</h4>
                  <p className="mt-0.5 text-xs text-ink-soft">
                    Choose which pipeline steps to run. Selected steps are forced to re-run even if
                    output already exists.
                  </p>
                </div>
                <fieldset className="flex flex-col gap-2" disabled={!canReprocess || reprocessing}>
                  {FULL_PIPELINE_STEPS.map((step) => {
                    const selectable = canSelectReprocessStep(step)
                    const checked = reprocessSteps.includes(step)
                    return (
                      // A div wrapping a label, not one label around
                      // everything: the model picker is itself a checkbox and
                      // a combobox, and nesting those in the step's label
                      // would make a click on either toggle the step.
                      <div
                        key={step}
                        // A stable hook for the browser suite, which asserts
                        // the model picker sits inside its own step's block.
                        data-reprocess-step={step}
                        className={`flex flex-col gap-2 rounded-xs border px-3 py-1.5 text-sm ${
                          selectable
                            ? 'border-line bg-bright text-ink-muted'
                            : 'border-line/50 bg-wash/50 text-ink-faint'
                        }`}
                      >
                        <label
                          className="flex items-start gap-2"
                          title={
                            step === 'extract_metadata' && !selectable
                              ? 'OCR text required, or select OCR'
                              : undefined
                          }
                        >
                          <input
                            type="checkbox"
                            className="mt-0.5"
                            checked={checked}
                            disabled={!selectable}
                            onChange={() => toggleReprocessStep(step)}
                          />
                          <span>
                            <span className="font-medium">{PROCESSING_STEP_LABELS[step]}</span>
                            <span className="mt-0.5 block text-xs font-normal text-ink-soft">
                              {PROCESSING_STEP_DESCRIPTIONS[step]}
                            </span>
                          </span>
                        </label>
                        {checked && (
                          <StepBindingOverride
                            step={step}
                            value={reprocessOverrides}
                            onChange={setReprocessOverrides}
                          />
                        )}
                      </div>
                    )
                  })}
                </fieldset>
                <Button
                  type="submit"
                  variant="danger"
                  size="sm"
                  className="self-start"
                  disabled={!canReprocess || reprocessing || reprocessSteps.length === 0}
                >
                  {reprocessing ? 'Reprocessing...' : 'Reprocess selected steps'}
                </Button>
              </form>
            </div>
          )}

          <form
            className="grid grid-cols-1 gap-4 rounded-none border border-line bg-surface p-5 sm:grid-cols-2"
            onSubmit={onSave}
          >
            <label className={labelClass}>
              Title
              <input
                className={fieldClass(editing)}
                readOnly={!editing}
                value={document.title ?? ''}
                onChange={(event) => setDocument({ ...document, title: event.target.value })}
              />
              {document.title_original && document.title_original !== document.title && (
                <span className="text-xs font-normal text-ink-soft">
                  Original: {document.title_original}
                </span>
              )}
            </label>

            <label className={labelClass}>
              Document date
              <input
                type="date"
                className={fieldClass(editing)}
                readOnly={!editing}
                value={document.document_date?.slice(0, 10) ?? ''}
                onChange={(event) => setDocument({ ...document, document_date: event.target.value })}
              />
            </label>

            <label className={labelClass}>
              Document type
              <input
                className={fieldClass(editing)}
                readOnly={!editing}
                value={documentTypeInput}
                onChange={(event) => setDocumentTypeInput(event.target.value)}
              />
              {document.expand?.document_type?.name_original &&
                document.expand.document_type.name_original !== document.expand.document_type.name && (
                  <span className="text-xs font-normal text-ink-soft">
                    Original: {document.expand.document_type.name_original}
                  </span>
                )}
            </label>

            <label className={labelClass}>
              Correspondent
              <input
                className={fieldClass(editing)}
                readOnly={!editing}
                value={correspondentInput}
                onChange={(event) => setCorrespondentInput(event.target.value)}
              />
              {document.expand?.correspondent?.name_original &&
                document.expand.correspondent.name_original !== document.expand.correspondent.name && (
                  <span className="text-xs font-normal text-ink-soft">
                    Original: {document.expand.correspondent.name_original}
                  </span>
                )}
            </label>

            <label className={`${labelClass} sm:col-span-2`}>
              Purpose
              <input
                className={fieldClass(editing)}
                readOnly={!editing}
                value={document.purpose ?? ''}
                onChange={(event) => setDocument({ ...document, purpose: event.target.value })}
              />
              {document.purpose_original && document.purpose_original !== document.purpose && (
                <span className="text-xs font-normal text-ink-soft">
                  Original: {document.purpose_original}
                </span>
              )}
            </label>

            <div className={`${labelClass} sm:col-span-2`}>
              <span>Tags</span>
              <TagField
                editing={editing}
                vocabulary={vocabulary}
                vocabularyError={vocabularyError}
                known={document.expand?.tags ?? []}
                selected={tagIds}
                onChange={setTagIds}
                suggestions={
                  !editing && document.processing_status === 'needs_review'
                    ? pendingTagSuggestions(
                        job,
                        (document.expand?.tags ?? []).map((tag) => tag.name),
                      )
                    : []
                }
                onAcceptSuggestion={(name) => void onAcceptSuggestedTag(name)}
                acceptingSuggestion={acceptingSuggestion}
              />
            </div>

            <label className={`${labelClass} sm:col-span-2`}>
              Summary
              <textarea
                rows={4}
                className={`${fieldClass(editing)} min-h-24 resize-y`}
                readOnly={!editing}
                value={document.summary ?? ''}
                onChange={(event) => setDocument({ ...document, summary: event.target.value })}
              />
              {document.summary_original && document.summary_original !== document.summary && (
                <span className="text-xs font-normal text-ink-soft">
                  Original: {document.summary_original}
                </span>
              )}
            </label>

            <div className={`${labelClass} sm:col-span-2`}>
              <div className="flex flex-wrap items-center justify-between gap-2">
                <span id="ocr-text-label">OCR text</span>
                {canTranslate && (
                  <div className="flex flex-wrap items-center justify-end gap-2">
                    {ocrView !== 'original' && (
                      <button
                        type="button"
                        disabled={translating}
                        onClick={() => void onRetranslate()}
                        className="rounded-xs border border-line-strong bg-surface px-3 py-1 text-xs font-medium text-ink-muted transition-colors hover:bg-bright disabled:opacity-50"
                      >
                        Re-translate
                      </button>
                    )}
                    <div role="group" aria-label="OCR text view" className="flex">
                      {(Object.keys(OCR_VIEW_LABELS) as OcrView[]).map((view) => (
                        <button
                          key={view}
                          type="button"
                          aria-pressed={ocrView === view}
                          onClick={() => setOcrView(view)}
                          className={`-ml-px border px-3 py-1 text-xs font-medium transition-colors first:ml-0 first:rounded-l-xs last:rounded-r-xs ${
                            view === 'both' ? 'max-sm:hidden' : ''
                          } ${view === 'translated' ? 'max-sm:rounded-r-xs' : ''} ${
                            ocrView === view
                              ? 'relative border-ink bg-ink text-paper hover:bg-oxblood'
                              : 'border-line-strong bg-surface text-ink-muted hover:bg-bright'
                          }`}
                        >
                          {OCR_VIEW_LABELS[view]}
                        </button>
                      ))}
                    </div>
                  </div>
                )}
              </div>
              <div
                className={sideBySide ? 'grid gap-3 sm:grid-cols-2' : 'flex'}
              >
                {!(translationWanted && ocrView === 'translated') && (
                  <textarea
                    ref={originalOcrRef}
                    rows={22}
                    readOnly={!editing}
                    aria-labelledby="ocr-text-label"
                    className={`${textareaClass(editing)} ${ocrTextareaClass}`}
                    onScroll={() => syncOcrScroll(originalOcrRef.current, translatedOcrRef.current)}
                    value={document.ocr_text ?? ''}
                    onChange={(event) => setDocument({ ...document, ocr_text: event.target.value })}
                  />
                )}
                {translationWanted && (
                  <textarea
                    ref={translatedOcrRef}
                    rows={22}
                    readOnly
                    aria-label={`OCR text translated to ${resultLanguage}`}
                    className={`${textareaClass(false)} ${ocrTextareaClass}`}
                    onScroll={() => syncOcrScroll(translatedOcrRef.current, originalOcrRef.current)}
                    value={translating ? 'Translating...' : (translation.data ?? '')}
                  />
                )}
              </div>
              {translationWanted && translation.error && !translating && (
                <span className="text-xs font-normal text-madder">{translation.error}</span>
              )}
              {editing && (
                <span className="text-xs font-normal text-ink-soft">
                  Everything else is derived from this text, so a correction here is worth more
                  than one to a single field. Saving re-indexes the document for search and queues
                  its passage vectors to be rebuilt; it does not re-run extraction -- reprocess
                  below for that, which reads the corrected text rather than re-running OCR.
                </span>
              )}
            </div>

            <div className="flex items-center gap-4 sm:col-span-2">
              {!owned ? (
                <p className="text-sm text-ink-soft">
                  Shared with you, read-only. Only its owner can correct it.
                </p>
              ) : editing ? (
                <Button type="submit" disabled={saving}>
                  {saving ? 'Saving...' : 'Save corrections'}
                </Button>
              ) : (
                <Button
                  onClick={(event) => {
                    // Without preventDefault, React swaps this node into the
                    // submit button before the browser applies the click's
                    // default action, submitting the form immediately.
                    event.preventDefault()
                    setEditing(true)
                  }}
                >
                  Unlock editing
                </Button>
              )}
              {message && <p className="text-sm text-forest">{message}</p>}
              {error && <p className="text-sm text-madder">{error}</p>}
            </div>
          </form>
        </div>

        {canPreview && previewMounted && (
          <aside
            className={`order-first w-2/5 shrink-0 sticky top-6 h-[calc(100vh-3rem)] ${
              showPreview ? '' : 'hidden'
            }`}
          >
            <DocumentPreview record={document} />
          </aside>
        )}
      </div>
    </section>
  )
}

/**
 * Tags are created only on /tags, so this offers what exists and nothing more,
 * and an empty vocabulary sends the reader there. The one exception is the
 * AI's suggestions on a document awaiting review: accepting one creates the
 * tag. The chips do not wait for the
 * vocabulary: the document's own expand carries the names it has, and a row
 * blanked mid-request would pretend a tagged document has no tags.
 */
function TagField({
  editing,
  vocabulary,
  vocabularyError,
  known,
  selected,
  onChange,
  suggestions,
  onAcceptSuggestion,
  acceptingSuggestion,
}: {
  editing: boolean
  /** null until the vocabulary loads, and if it fails. */
  vocabulary: TagRecord[] | null
  vocabularyError: string
  /** The document's own expanded tags, so chips render without the vocabulary. */
  known: TagRecord[]
  selected: string[]
  onChange: (next: string[]) => void
  suggestions: string[]
  onAcceptSuggestion: (name: string) => void
  acceptingSuggestion: boolean
}) {
  const byId = new Map([...known, ...(vocabulary ?? [])].map((tag) => [tag.id, tag]))
  // An id with no name behind it is a tag deleted since the document loaded.
  // Shown as a placeholder rather than dropped, because saving writes
  // `selected`, so a silently missing chip would be written back anyway.
  const chosen: TagRecord[] = selected.map((id) => byId.get(id) ?? { id, name: 'Deleted tag' })
  const available = (vocabulary ?? []).filter((tag) => !selected.includes(tag.id))

  return (
    <div className="flex flex-col gap-2">
      {chosen.length === 0 ? (
        <p className="text-sm font-normal text-ink-soft">No tags.</p>
      ) : (
        <ul className="flex flex-wrap gap-1.5">
          {chosen.map((tag) => (
            <li
              key={tag.id}
              className="flex items-center gap-1 rounded-xs border border-line-strong bg-wash px-2 py-1 text-xs font-normal text-ink"
              style={{ borderColor: tag.color || undefined }}
            >
              {tag.name}
              {editing && (
                <button
                  type="button"
                  aria-label={`Remove ${tag.name}`}
                  className="text-ink-faint transition-colors hover:text-madder"
                  onClick={() => onChange(selected.filter((id) => id !== tag.id))}
                >
                  &times;
                </button>
              )}
            </li>
          ))}
        </ul>
      )}

      <SuggestedTags names={suggestions} disabled={acceptingSuggestion} onAccept={onAcceptSuggestion} />

      {editing && vocabularyError && (
        <p className="text-sm font-normal text-madder">
          {vocabularyError}. Tags already on this document can still be removed.
        </p>
      )}

      {editing &&
        vocabulary !== null &&
        (vocabulary.length === 0 ? (
          <p className="text-sm font-normal text-ink-soft">
            You have no tags yet.{' '}
            {/* A new tab on purpose: this renders mid-edit, and navigating away
                from a half-corrected document throws the corrections away. */}
            <Link to="/tags" target="_blank" rel="noopener noreferrer" className="text-oxblood underline">
              Create some
            </Link>{' '}
            and they will be offered here.
          </p>
        ) : (
          available.length > 0 && (
            <Combobox
              value=""
              options={available.map((tag) => ({ value: tag.id, label: tag.name }))}
              placeholder="Add a tag..."
              ariaLabel="Add a tag"
              className="max-w-xs"
              onChange={(id) => onChange([...selected, id])}
            />
          )
        ))}
    </div>
  )
}

const labelClass = 'flex flex-col gap-1.5 text-sm font-medium text-ink-muted'
const inputClass =
  'w-full rounded-xs border border-line-strong bg-surface px-3 py-2 text-sm font-normal text-ink outline-none placeholder:text-ink-faint focus:border-oxblood focus:ring-1 focus:ring-oxblood'
const readonlyClass =
  'cursor-default border-line bg-wash/50 text-ink-muted shadow-inner focus:border-line focus:ring-0'

function fieldClass(editing: boolean) {
  return editing ? inputClass : `${inputClass} ${readonlyClass}`
}

function textareaClass(editing: boolean) {
  return `${fieldClass(editing)} min-h-48 resize-y`
}

// Tailwind's xl, as a media query.
function previewViewport(): MediaQueryList {
  return window.matchMedia('(min-width: 80rem)')
}
