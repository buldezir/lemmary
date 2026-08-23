import { type SubmitEvent, useEffect, useRef, useState } from 'react'
import { Link, useNavigate, useParams } from '@tanstack/react-router'
import { ClientResponseError } from 'pocketbase'
import { pb } from '../lib/pb'
import { ensureAuth } from '../lib/auth'
import {
  fileUrl,
  reprocessDocument,
  saveDocumentMetadata,
  type DocumentRecord,
} from '../lib/api/documents'
import {
  defaultReprocessSteps,
  forceStepsForReprocess,
  FULL_PIPELINE_STEPS,
  orderedProcessingSteps,
  PROCESSING_STEP_DESCRIPTIONS,
  PROCESSING_STEP_LABELS,
  type ProcessingJobRecord,
  type ProcessingStep,
} from '../lib/processing'
import { Button } from '../components/ui'

export function DocumentDetailPage() {
  const { documentId } = useParams({ from: '/document/$documentId' })
  const navigate = useNavigate()
  const [document, setDocument] = useState<DocumentRecord | null>(null)
  const [job, setJob] = useState<ProcessingJobRecord | null>(null)
  const [tagInput, setTagInput] = useState('')
  const [documentTypeInput, setDocumentTypeInput] = useState('')
  const [correspondentInput, setCorrespondentInput] = useState('')
  const [loading, setLoading] = useState(true)
  const [editing, setEditing] = useState(false)
  const [saving, setSaving] = useState(false)
  const [reprocessing, setReprocessing] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [reprocessSteps, setReprocessSteps] = useState<ProcessingStep[]>([])
  const [showProcessingJob, setShowProcessingJob] = useState(false)
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')

  // Mirrors `editing` for the load callback below: a background refresh (poll
  // or realtime event) must not clobber the form while the user is typing.
  const editingRef = useRef(editing)
  useEffect(() => {
    editingRef.current = editing
  }, [editing])

  // Tracks which (document, has-OCR-text) pair the reprocess defaults were
  // computed for, so background refreshes do not reset the user's selection.
  const reprocessDefaultsKey = useRef('')

  function applyLoadedDocument(doc: DocumentRecord) {
    setDocument(doc)
    setTagInput((doc.expand?.tags ?? []).map((tag) => tag.name).join(', '))
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
        if (!editingRef.current) {
          applyLoadedDocument(doc)
        }
        setError('')

        const inFlight = doc.processing_status === 'processing' || doc.processing_status === 'pending'
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

  const canReprocess =
    document?.processing_status !== 'processing' && document?.processing_status !== 'pending'

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
    const confirmed = window.confirm(
      `Re-run these steps?\n\n${stepLabels}\n\nExisting metadata may be overwritten.`,
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
      await reprocessDocument(document.id, steps, forceStepsForReprocess(steps))

      const doc = await pb.collection('documents').getOne<DocumentRecord>(document.id, {
        expand: 'tags,document_type,correspondent,duplicate_of',
      })
      const jobs = await pb.collection('processing_jobs').getList<ProcessingJobRecord>(1, 1, {
        filter: pb.filter('document = {:documentId}', { documentId: document.id }),
        sort: '-created',
      })

      applyLoadedDocument(doc)
      setJob(jobs.items[0] ?? null)
      setMessage(
        `Document queued for reprocessing (${steps.map((step) => PROCESSING_STEP_LABELS[step]).join(', ')}).`,
      )
    } catch (err) {
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

    await navigate({ to: '/' })
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
        documentDate: document.document_date,
        documentTypeName: documentTypeInput,
        correspondentName: correspondentInput,
        tagNames: tagInput.split(','),
        processingStatus: document.processing_status,
      })

      setEditing(false)
      editingRef.current = false
      const refreshed = await pb.collection('documents').getOne<DocumentRecord>(document.id, {
        expand: 'tags,document_type,correspondent,duplicate_of',
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
    return <p className="text-sm text-stone-500">Loading document...</p>
  }

  if (!document) {
    return (
      <section className="flex flex-col gap-3">
        <p className="text-sm text-red-600">{error || 'Document not found.'}</p>
        <Link to="/" className="text-sm font-medium text-gray-900 underline">
          Back to documents
        </Link>
      </section>
    )
  }

  return (
    <section className="flex flex-col gap-6">
      {document.duplicate_of && (
        <div className="rounded-md border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-900">
          Possible duplicate of{' '}
          <Link
            to="/document/$documentId"
            params={{ documentId: document.duplicate_of }}
            className="font-medium underline"
          >
            {document.expand?.duplicate_of?.title?.trim() || document.duplicate_of}
          </Link>
          . Review both documents and delete the one you do not need.
        </div>
      )}
      <div className="flex items-start justify-between gap-4">
        <div>
          <Link to="/" className="text-sm text-stone-500 hover:text-stone-950">
            &larr; Back to documents
          </Link>
          <h2 className="mt-1 text-xl font-semibold text-stone-950">
            {document.title || 'Untitled document'}
          </h2>
          <p className="text-sm text-stone-500">Status: {document.processing_status}</p>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <Link
            to="/document/$documentId/ask"
            params={{ documentId }}
            aria-disabled={!hasOcrText}
            title={
              hasOcrText ? 'Ask questions about this document' : 'OCR text required before asking AI'
            }
            className={`rounded-md border px-4 py-2 text-sm font-medium transition-colors ${
              hasOcrText
                ? 'border-gray-900 bg-gray-900 text-white hover:bg-gray-700'
                : 'pointer-events-none border-stone-200 bg-stone-100 text-stone-400'
            }`}
          >
            Ask AI
          </Link>
          {document.file && (
            <a
              className="rounded-md border border-stone-300 bg-stone-50 px-4 py-2 text-sm font-medium text-stone-700 transition-colors hover:bg-white"
              href={fileUrl(document)}
              target="_blank"
              rel="noreferrer"
            >
              Open file
            </a>
          )}
          <Button variant="danger" onClick={() => void onDelete()} disabled={deleting}>
            {deleting ? 'Deleting...' : 'Delete'}
          </Button>
          {job ? (
            <button
              type="button"
              onClick={() => setShowProcessingJob((visible) => !visible)}
              aria-label={showProcessingJob ? 'Hide processing job details' : 'Show processing job details'}
              aria-pressed={showProcessingJob}
              title={showProcessingJob ? 'Hide processing job' : 'Show processing job'}
              className={`flex h-9 w-9 shrink-0 items-center justify-center rounded-md border transition-colors ${
                showProcessingJob
                  ? 'border-gray-900 bg-gray-900 text-white hover:bg-gray-700'
                  : 'border-stone-300 bg-stone-50 text-stone-500 hover:bg-white hover:text-stone-700'
              }`}
            >
              <svg
                xmlns="http://www.w3.org/2000/svg"
                viewBox="0 0 20 20"
                fill="currentColor"
                className="h-4 w-4"
                aria-hidden="true"
              >
                <path
                  fillRule="evenodd"
                  d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-7-4a1 1 0 11-2 0 1 1 0 012 0zM9 9a.75.75 0 000 1.5h.253a.25.25 0 01.244.304l-.459 2.066A1.75 1.75 0 0010.747 15H11a.75.75 0 000-1.5h-.253a.25.25 0 01-.244-.304l.459-2.066A1.75 1.75 0 009.253 9H9z"
                  clipRule="evenodd"
                />
              </svg>
            </button>
          ) : null}
        </div>
      </div>

      {job && showProcessingJob && (
        <div className="rounded-lg border border-stone-200 bg-stone-50 p-3">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
            <h3 className="text-sm font-semibold text-stone-950">Processing job</h3>
            <span className="rounded bg-stone-200 px-1.5 py-0.5 text-xs font-medium text-stone-700">
              {job.status}
            </span>
            {job.current_step ? (
              <span className="text-xs text-stone-500">current: {job.current_step}</span>
            ) : null}
            <span className="text-xs text-stone-500">
              {(job.steps ?? []).join(' → ') || 'n/a'}
            </span>
          </div>
          {job.step_runs && job.step_runs.length > 0 ? (
            <ul className="mt-2 flex flex-col gap-1 text-sm text-stone-700">
              {job.step_runs.map((run) => (
                <li key={run.name} className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
                  <span className="font-medium">{run.name}</span>
                  <span className="rounded bg-stone-200 px-1.5 py-0.5 text-xs">{run.status}</span>
                  {run.attempts > 0 ? (
                    <span className="text-xs text-stone-500">attempts: {run.attempts}</span>
                  ) : null}
                  {run.provider ? (
                    <span className="text-xs text-stone-500">provider: {run.provider}</span>
                  ) : null}
                  {run.model ? (
                    <span className="text-xs text-stone-500">model: {run.model}</span>
                  ) : null}
                  {run.prompt_version ? (
                    <span className="text-xs text-stone-500">prompt: {run.prompt_version}</span>
                  ) : null}
                  {run.error ? <span className="text-xs text-red-600">{run.error}</span> : null}
                </li>
              ))}
            </ul>
          ) : null}

          <form
            className="mt-3 flex flex-col gap-2 border-t border-stone-200 pt-3"
            onSubmit={onReprocessSubmit}
          >
            <div>
              <h4 className="text-sm font-semibold text-stone-950">Reprocess</h4>
              <p className="mt-0.5 text-xs text-stone-500">
                Choose which pipeline steps to run. Selected steps are forced to re-run even if
                output already exists.
              </p>
            </div>
            <fieldset className="flex flex-col gap-2" disabled={!canReprocess || reprocessing}>
              {FULL_PIPELINE_STEPS.map((step) => {
                const selectable = canSelectReprocessStep(step)
                const checked = reprocessSteps.includes(step)
                return (
                  <label
                    key={step}
                    className={`flex items-start gap-2 rounded-md border px-3 py-1.5 text-sm ${
                      selectable
                        ? 'border-stone-200 bg-white text-stone-700'
                        : 'border-stone-100 bg-stone-100/80 text-stone-400'
                    }`}
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
                      <span className="mt-0.5 block text-xs font-normal text-stone-500">
                        {PROCESSING_STEP_DESCRIPTIONS[step]}
                      </span>
                    </span>
                  </label>
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
        className="grid grid-cols-1 gap-4 rounded-lg border border-stone-200 bg-stone-50 p-6 sm:grid-cols-2"
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
            <span className="text-xs font-normal text-stone-500">
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
              <span className="text-xs font-normal text-stone-500">
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
              <span className="text-xs font-normal text-stone-500">
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
            <span className="text-xs font-normal text-stone-500">
              Original: {document.purpose_original}
            </span>
          )}
        </label>

        <label className={`${labelClass} sm:col-span-2`}>
          Tags (comma separated)
          <input
            className={fieldClass(editing)}
            readOnly={!editing}
            value={tagInput}
            onChange={(event) => setTagInput(event.target.value)}
          />
        </label>

        <label className={`${labelClass} sm:col-span-2`}>
          Summary
          <textarea
            rows={8}
            className={textareaClass(editing)}
            readOnly={!editing}
            value={document.summary ?? ''}
            onChange={(event) => setDocument({ ...document, summary: event.target.value })}
          />
          {document.summary_original && document.summary_original !== document.summary && (
            <span className="text-xs font-normal text-stone-500">
              Original: {document.summary_original}
            </span>
          )}
        </label>

        <label className={`${labelClass} sm:col-span-2`}>
          OCR text
          <textarea
            rows={18}
            readOnly
            className={`${textareaClass(false)} min-h-96 font-mono text-xs leading-relaxed cursor-not-allowed`}
            value={document.ocr_text ?? ''}
          />
        </label>

        <div className="flex items-center gap-4 sm:col-span-2">
          {editing ? (
            <Button type="submit" disabled={saving}>
              {saving ? 'Saving...' : 'Save corrections'}
            </Button>
          ) : (
            <Button
              onClick={(event) => {
                // Without preventDefault, React swaps this node into the
                // submit button before the browser applies the click's default
                // action, which would submit the form immediately.
                event.preventDefault()
                setEditing(true)
              }}
            >
              Edit
            </Button>
          )}
          {message && <p className="text-sm text-green-600">{message}</p>}
          {error && <p className="text-sm text-red-600">{error}</p>}
        </div>
      </form>
    </section>
  )
}

const labelClass = 'flex flex-col gap-1.5 text-sm font-medium text-stone-700'
const inputClass =
  'w-full rounded-md border border-stone-300 bg-stone-50 px-3 py-2 text-sm font-normal text-stone-950 outline-none placeholder:text-stone-400 focus:border-gray-900 focus:ring-1 focus:ring-gray-900'
const readonlyClass =
  'cursor-default border-stone-200 bg-stone-100/80 text-stone-700 shadow-inner focus:border-stone-200 focus:ring-0'

function fieldClass(editing: boolean) {
  return editing ? inputClass : `${inputClass} ${readonlyClass}`
}

function textareaClass(editing: boolean) {
  return `${fieldClass(editing)} min-h-48 resize-y`
}
