import { type DragEvent, type SubmitEvent, useRef, useState } from 'react'
import { Link, useNavigate } from '@tanstack/react-router'
import { pb } from '../lib/pb'
import { ensureAuth } from '../lib/auth'
import { parseDuplicateOfId } from '../lib/api/documents'
import { limitFromError, type LimitName } from '../lib/api/limits'
import { documentsLanding } from '../lib/reviewPolicy'
import { filesFromDataTransfer, withFolderName } from '../lib/fileDrop'
import { Button } from '../components/ui'

// The documents.file allowlist (see the migrations), as the one thing the three
// forms below are derived from. Mirrors `storable` in
// backend/internal/zipimport; the server decides by sniffing content, so this is
// what to offer the file picker, not what the collection will ultimately take.
const ACCEPTED: Record<string, string> = {
  '.pdf': 'application/pdf',
  '.jpg': 'image/jpeg',
  '.jpeg': 'image/jpeg',
  '.png': 'image/png',
  '.webp': 'image/webp',
  '.txt': 'text/plain',
  '.csv': 'text/csv',
  '.docx': 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
  '.xlsx': 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
}
const ACCEPTED_MIME_TYPES = new Set(Object.values(ACCEPTED))
const SUPPORTED_FORMATS_LABEL =
  'PDF, JPEG, PNG, WebP, plain text, CSV, Word (.docx), or Excel (.xlsx)'

const ACCEPT_ATTR = [...Object.keys(ACCEPTED), ...ACCEPTED_MIME_TYPES].join(',')

type FileUploadError = {
  name: string
  message: string
  duplicateOfId: string | null
}

function extensionOf(file: File) {
  return file.name.includes('.') ? file.name.slice(file.name.lastIndexOf('.')).toLowerCase() : ''
}

function isAcceptedFile(file: File) {
  if (ACCEPTED[extensionOf(file)]) return true
  return ACCEPTED_MIME_TYPES.has(file.type)
}

function fileKey(file: File) {
  return `${file.name}:${file.size}:${file.lastModified}`
}

function uploadErrorMessage(err: unknown): string {
  if (err && typeof err === 'object') {
    const withResponse = err as {
      message?: string
      response?: { message?: string }
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

/**
 * The limits that bound the whole instance, as opposed to one upload. Hitting
 * one of these means no further file can succeed either.
 */
const INSTANCE_WIDE_LIMITS = new Set<LimitName>([
  'documents',
  'document_pages',
  'storage_bytes',
])

function formatBytes(size: number) {
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`
  return `${(size / (1024 * 1024)).toFixed(1)} MB`
}

export function UploadFilesPage() {
  const navigate = useNavigate()
  const inputRef = useRef<HTMLInputElement>(null)
  const folderInputRef = useRef<HTMLInputElement>(null)
  const [files, setFiles] = useState<File[]>([])
  // A zip is not an unsupported file, it is the wrong page -- so it gets a link
  // rather than the "use PDF, JPEG, ..." message.
  const [zipRejected, setZipRejected] = useState(false)
  const [uploading, setUploading] = useState(false)
  const [uploadIndex, setUploadIndex] = useState(0)
  const [dragging, setDragging] = useState(false)
  const [error, setError] = useState('')
  const [fileErrors, setFileErrors] = useState<FileUploadError[]>([])

  function resetInput() {
    if (inputRef.current) inputRef.current.value = ''
    if (folderInputRef.current) folderInputRef.current.value = ''
  }

  function selectFiles(next: FileList | File[] | null) {
    if (!next || next.length === 0) {
      resetInput()
      return
    }

    // A file picked inside a folder keeps that folder as a name prefix, on the
    // same rule the zip import uses, so a tree of scanner output does not land
    // as a page of identical 1.pdfs.
    const incoming = Array.from(next).map((file) =>
      file.webkitRelativePath ? withFolderName(file, file.webkitRelativePath) : file,
    )
    const accepted: File[] = []
    const rejected: string[] = []
    let zipped = false

    const seen = new Set(files.map(fileKey))
    for (const file of incoming) {
      if (!isAcceptedFile(file)) {
        if (extensionOf(file) === '.zip') zipped = true
        else rejected.push(file.name)
        continue
      }
      const key = fileKey(file)
      if (seen.has(key)) continue
      seen.add(key)
      accepted.push(file)
    }

    // Appending, not replacing: a folder and then a stray file is one upload as
    // far as the person doing it is concerned.
    setFiles((current) => [...current, ...accepted])

    setFileErrors([])
    if (zipped) {
      setZipRejected(true)
      setError('')
    } else if (rejected.length > 0) {
      setZipRejected(false)
      setError(
        rejected.length === 1
          ? `Unsupported file type (${rejected[0]}). Use ${SUPPORTED_FORMATS_LABEL}.`
          : `Unsupported file types (${rejected.join(', ')}). Use ${SUPPORTED_FORMATS_LABEL}.`,
      )
    } else {
      setZipRejected(false)
      setError('')
    }
  }

  function removeFile(index: number) {
    setFiles((current) => current.filter((_, i) => i !== index))
    setFileErrors((current) => current.filter((_, i) => i !== index))
    resetInput()
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
    // Folders only exist through the entry API, and it has to be read before
    // this handler returns -- filesFromDataTransfer does that part first.
    void filesFromDataTransfer(event.dataTransfer).then(selectFiles)
  }

  async function onSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    if (files.length === 0) {
      setError('Choose at least one file to upload.')
      return
    }

    try {
      setUploading(true)
      setError('')
      setFileErrors([])
      setUploadIndex(1)
      await ensureAuth()

      const userId = pb.authStore.record?.id ?? ''
      const uploadedIds: string[] = []
      const failures: FileUploadError[] = []
      const failedFiles: File[] = []
      // Set when an instance allowance ran out and the loop stopped early, so
      // the summary can say files were not attempted rather than implying they
      // were tried and failed.
      let stoppedAt = -1

      for (let i = 0; i < files.length; i++) {
        const file = files[i]
        setUploadIndex(i + 1)
        try {
          const formData = new FormData()
          formData.append('file', file)
          formData.append('user', userId)
          formData.append('processing_status', 'pending')
          const record = await pb.collection('documents').create(formData)
          uploadedIds.push(record.id)
        } catch (err) {
          const message = uploadErrorMessage(err)
          failures.push({
            name: file.name,
            message,
            duplicateOfId: duplicateIdFromError(err, message),
          })
          failedFiles.push(file)

          // An instance-wide allowance ran out, so every remaining file would
          // be refused for the same reason. Stop and keep them staged rather
          // than printing the same rejection once per file. A per-file limit
          // (this one is too big) says nothing about the next file, so those
          // keep going.
          const limit = limitFromError(err)
          if (limit && INSTANCE_WIDE_LIMITS.has(limit)) {
            failedFiles.push(...files.slice(i + 1))
            stoppedAt = i
            break
          }
        }
      }

      if (failures.length === 0) {
        if (uploadedIds.length === 1) {
          navigate({ to: '/document/$documentId', params: { documentId: uploadedIds[0] } })
        } else {
          navigate({ to: documentsLanding() })
        }
        return
      }

      setFiles(failedFiles)
      setFileErrors(failures)
      resetInput()
      if (stoppedAt >= 0) {
        const notAttempted = files.length - stoppedAt - 1
        const uploaded = `Uploaded ${uploadedIds.length} of ${files.length} files.`
        setError(
          notAttempted > 0
            ? `${uploaded} This instance ran out of room, so ${notAttempted} more ${notAttempted === 1 ? 'was' : 'were'} not attempted.`
            : `${uploaded} This instance ran out of room.`,
        )
      } else if (uploadedIds.length > 0) {
        setError(
          `Uploaded ${uploadedIds.length} of ${files.length} files. ${failures.length} failed.`,
        )
      }
    } finally {
      setUploading(false)
      setUploadIndex(0)
    }
  }

  const dropLabel =
    files.length === 0
      ? 'Choose files'
      : files.length === 1
        ? files[0].name
        : `${files.length} files selected`

  return (
    <section className="flex flex-col gap-5">
      <div>
        <h2 className="font-display text-xl font-semibold text-ink">Upload documents</h2>
        <p className="text-sm text-ink-soft">Supported formats: {SUPPORTED_FORMATS_LABEL}.</p>
      </div>

      <form className="flex flex-col gap-4" onSubmit={onSubmit}>
        <label
          className={`flex min-h-44 cursor-pointer flex-col items-center justify-center gap-1 rounded-none border border-dashed p-6 text-center transition-colors ${
            dragging
              ? 'border-ink bg-bright'
              : 'border-line-strong bg-surface hover:border-ink/50 hover:bg-bright'
          }`}
          onDragOver={onDragOver}
          onDragEnter={onDragEnter}
          onDragLeave={onDragLeave}
          onDrop={onDrop}
        >
          <input
            ref={inputRef}
            type="file"
            multiple
            accept={ACCEPT_ATTR}
            onChange={(event) => selectFiles(event.target.files)}
            className="hidden"
            id="file-upload"
          />
          <span className="text-sm font-medium text-ink">{dropLabel}</span>
          <span className="text-xs text-ink-faint">or drop files and folders here</span>
        </label>

        {/* An input cannot offer files and folders at once, so the folder
            picker is its own control. Dropping needs no such split. */}
        <div className="-mt-2">
          <input
            ref={folderInputRef}
            type="file"
            multiple
            {...({ webkitdirectory: '', directory: '' } as Record<string, string>)}
            onChange={(event) => selectFiles(event.target.files)}
            className="hidden"
            id="folder-upload"
          />
          <label
            htmlFor="folder-upload"
            className="cursor-pointer text-xs font-medium text-ink-soft underline hover:text-ink"
          >
            Choose a folder instead
          </label>
        </div>

        {files.length > 0 && (
          <ul className="flex flex-col gap-2">
            {files.map((file, index) => {
              const fileError = fileErrors[index]
              return (
                <li
                  key={fileKey(file)}
                  className="flex items-start justify-between gap-3 rounded-xs border border-line bg-bright px-3 py-2"
                >
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-sm font-medium text-ink">{file.name}</p>
                    <p className="text-xs text-ink-faint">{formatBytes(file.size)}</p>
                    {fileError && (
                      <div className="mt-1 flex flex-col gap-1 text-sm text-madder">
                        <p>{fileError.message}</p>
                        {fileError.duplicateOfId && (
                          <Link
                            to="/document/$documentId"
                            params={{ documentId: fileError.duplicateOfId }}
                            className="font-medium text-oxblood underline"
                          >
                            Open existing document
                          </Link>
                        )}
                      </div>
                    )}
                  </div>
                  <button
                    type="button"
                    onClick={() => removeFile(index)}
                    disabled={uploading}
                    aria-label={`Remove ${file.name}`}
                    className="shrink-0 text-xs font-medium text-ink-soft hover:text-ink disabled:opacity-50"
                  >
                    Remove
                  </button>
                </li>
              )
            })}
          </ul>
        )}

        {zipRejected && (
          <p className="text-sm text-ink-soft">
            That is a zip archive.{' '}
            <Link to="/upload/zip" className="font-medium text-oxblood underline">
              Import it on the Zip archive tab
            </Link>{' '}
            to see what it holds before anything is imported.
          </p>
        )}

        {error && <p className="text-sm text-madder">{error}</p>}

        <Button type="submit" disabled={uploading || files.length === 0}>
          {uploading
            ? files.length > 1
              ? `Uploading ${uploadIndex} of ${files.length}...`
              : 'Uploading...'
            : 'Upload and process'}
        </Button>
      </form>
    </section>
  )
}
