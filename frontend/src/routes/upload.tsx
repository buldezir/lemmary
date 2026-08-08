import { type DragEvent, type SubmitEvent, useState } from 'react'
import { Link, useNavigate } from '@tanstack/react-router'
import { ensureAuth, parseDuplicateOfId, pb } from '../lib/pocketbase'

const ACCEPTED_EXTENSIONS = new Set([
  '.pdf',
  '.jpg',
  '.jpeg',
  '.png',
  '.webp',
  '.txt',
  '.csv',
  '.docx',
  '.xlsx',
])
const ACCEPTED_MIME_TYPES = new Set([
  'application/pdf',
  'image/jpeg',
  'image/png',
  'image/webp',
  'text/plain',
  'text/csv',
  'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
  'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
])
const SUPPORTED_FORMATS_LABEL =
  'PDF, JPEG, PNG, WebP, plain text, CSV, Word (.docx), or Excel (.xlsx)'

function isAcceptedFile(file: File) {
  const extension = file.name.includes('.') ? file.name.slice(file.name.lastIndexOf('.')).toLowerCase() : ''
  if (ACCEPTED_EXTENSIONS.has(extension)) return true
  return ACCEPTED_MIME_TYPES.has(file.type)
}

function uploadErrorMessage(err: unknown): string {
  if (err && typeof err === 'object') {
    const withResponse = err as {
      message?: string
      response?: { message?: string; data?: { duplicate_of?: string } }
    }
    if (withResponse.response?.message) return withResponse.response.message
    if (typeof withResponse.message === 'string' && withResponse.message) return withResponse.message
  }
  if (err instanceof Error) return err.message
  return 'Upload failed'
}

function duplicateIdFromError(err: unknown, message: string): string | null {
  if (err && typeof err === 'object') {
    const data = (err as { response?: { data?: { duplicate_of?: string } } }).response?.data
    if (typeof data?.duplicate_of === 'string' && data.duplicate_of) {
      return data.duplicate_of
    }
  }
  return parseDuplicateOfId(message)
}

export function UploadPage() {
  const navigate = useNavigate()
  const [file, setFile] = useState<File | null>(null)
  const [uploading, setUploading] = useState(false)
  const [dragging, setDragging] = useState(false)
  const [error, setError] = useState('')
  const [duplicateOfId, setDuplicateOfId] = useState<string | null>(null)

  function selectFile(next: File | null) {
    if (!next) {
      setFile(null)
      return
    }
    if (!isAcceptedFile(next)) {
      setError(`Unsupported file type. Use ${SUPPORTED_FORMATS_LABEL}.`)
      setDuplicateOfId(null)
      return
    }
    setError('')
    setDuplicateOfId(null)
    setFile(next)
  }

  function onDragOver(event: DragEvent<HTMLLabelElement>) {
    event.preventDefault()
  }

  function onDragEnter(event: DragEvent<HTMLLabelElement>) {
    event.preventDefault()
    setDragging(true)
  }

  function onDragLeave(event: DragEvent<HTMLLabelElement>) {
    event.preventDefault()
    if (event.currentTarget.contains(event.relatedTarget as Node)) return
    setDragging(false)
  }

  function onDrop(event: DragEvent<HTMLLabelElement>) {
    event.preventDefault()
    setDragging(false)
    selectFile(event.dataTransfer.files?.[0] ?? null)
  }

  async function onSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!file) {
      setError('Choose a file to upload.')
      return
    }

    try {
      setUploading(true)
      setError('')
      setDuplicateOfId(null)
      await ensureAuth()

      const formData = new FormData()
      formData.append('file', file)
      formData.append('user', pb.authStore.record?.id ?? '')
      formData.append('processing_status', 'pending')

      const record = await pb.collection('documents').create(formData)
      navigate({ to: '/document/$documentId', params: { documentId: record.id } })
    } catch (err) {
      const message = uploadErrorMessage(err)
      setError(message)
      setDuplicateOfId(duplicateIdFromError(err, message))
    } finally {
      setUploading(false)
    }
  }

  return (
    <section className="mx-auto flex max-w-xl flex-col gap-6">
      <div>
        <h2 className="text-xl font-semibold text-stone-950">Upload document</h2>
        <p className="text-sm text-stone-500">Supported formats: {SUPPORTED_FORMATS_LABEL}.</p>
      </div>

      <form className="flex flex-col gap-4" onSubmit={onSubmit}>
        <label
          className={`flex min-h-44 cursor-pointer flex-col items-center justify-center gap-1 rounded-lg border border-dashed p-6 text-center transition-colors ${
            dragging
              ? 'border-gray-900 bg-white'
              : 'border-stone-300 bg-stone-50 hover:border-stone-400 hover:bg-white'
          }`}
          onDragOver={onDragOver}
          onDragEnter={onDragEnter}
          onDragLeave={onDragLeave}
          onDrop={onDrop}
        >
          <input
            type="file"
            accept=".pdf,.jpg,.jpeg,.png,.webp,.txt,.csv,.docx,.xlsx,application/pdf,image/jpeg,image/png,image/webp,text/plain,text/csv,application/vnd.openxmlformats-officedocument.wordprocessingml.document,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
            onChange={(event) => selectFile(event.target.files?.[0] ?? null)}
            className="hidden"
          />
          <span className="text-sm font-medium text-stone-950">
            {file ? file.name : 'Choose a file'}
          </span>
          {!file && <span className="text-xs text-stone-400">or drop it here</span>}
        </label>

        {error && (
          <div className="flex flex-col gap-1 text-sm text-red-600">
            <p>{error}</p>
            {duplicateOfId && (
              <Link
                to="/document/$documentId"
                params={{ documentId: duplicateOfId }}
                className="font-medium text-gray-900 underline"
              >
                Open existing document
              </Link>
            )}
          </div>
        )}

        <button
          type="submit"
          disabled={uploading || !file}
          className="rounded-md bg-gray-900 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-gray-700 disabled:cursor-not-allowed disabled:opacity-50"
        >
          {uploading ? 'Uploading...' : 'Upload and process'}
        </button>
      </form>
    </section>
  )
}
