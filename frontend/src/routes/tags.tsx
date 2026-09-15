import { type SubmitEvent, useState } from 'react'
import { Link } from '@tanstack/react-router'
import {
  assignTagsWithAI,
  createTag,
  deleteTag,
  listTags,
  previewTagAssign,
  renameTag,
  type TagRecord,
} from '../lib/api/tags'
import { useAsync } from '../hooks/useAsync'
import {
  Button,
  fieldHintClassName,
  inputClassName,
  labelClassName,
  labelTextClassName,
  sectionClassName,
  sectionTitleClassName,
} from '../components/ui'

type TagRowProps = {
  tag: TagRecord
  busy: boolean
  onRename: (id: string, name: string) => Promise<void>
  onDelete: (tag: TagRecord) => Promise<void>
  onAssign: (tag: TagRecord) => Promise<void>
}

function TagRow({ tag, busy, onRename, onDelete, onAssign }: TagRowProps) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(tag.name)

  async function onSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    const name = draft.trim()
    if (!name || name === tag.name) {
      setEditing(false)
      return
    }
    await onRename(tag.id, name)
    setEditing(false)
  }

  return (
    <li className="flex flex-wrap items-center justify-between gap-2 rounded-xs border border-line bg-bright px-3 py-2">
      {editing ? (
        <form className="flex flex-1 flex-wrap items-center gap-2" onSubmit={onSubmit}>
          <input
            aria-label="Tag name"
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            className={`${inputClassName} max-w-xs flex-1`}
          />
          <Button type="submit" size="xs" disabled={busy}>
            Save
          </Button>
          <Button
            size="xs"
            variant="secondary"
            disabled={busy}
            onClick={() => {
              setDraft(tag.name)
              setEditing(false)
            }}
          >
            Cancel
          </Button>
        </form>
      ) : (
        <>
          <p className="min-w-0 truncate text-sm font-medium text-ink">{tag.name}</p>
          <div className="flex flex-wrap items-center gap-2">
            <Link
              to="/"
              search={{ q: tag.name }}
              className="rounded-xs border border-line px-2 py-1 text-xs text-ink-soft transition-colors hover:text-ink"
            >
              Find documents
            </Link>
            <Button size="xs" variant="secondary" disabled={busy} onClick={() => void onAssign(tag)}>
              Assign with AI
            </Button>
            <Button
              size="xs"
              variant="secondary"
              disabled={busy}
              onClick={() => {
                setDraft(tag.name)
                setEditing(true)
              }}
            >
              Rename
            </Button>
            <Button size="xs" variant="danger" disabled={busy} onClick={() => void onDelete(tag)}>
              Delete
            </Button>
          </div>
        </>
      )}
    </li>
  )
}

/**
 * The tag vocabulary, and the only place it grows: extraction picks from this
 * list and never adds to it, so an archive with no tags here gets none at all.
 */
export function TagsPage() {
  const { data: tags, loading, error: loadError, reload } = useAsync(listTags, [])
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  const rows = tags ?? []

  async function onCreate(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    const next = name.trim()
    if (!next) {
      return
    }
    try {
      setBusy(true)
      setError('')
      setNotice('')
      await createTag(next)
      await reload()
      setName('')
      setNotice(`Added "${next}".`)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to create the tag')
    } finally {
      setBusy(false)
    }
  }

  async function onRename(id: string, nextName: string) {
    try {
      setBusy(true)
      setError('')
      setNotice('')
      await renameTag(id, nextName)
      await reload()
      setNotice(`Renamed to "${nextName}".`)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to rename the tag')
    } finally {
      setBusy(false)
    }
  }

  /**
   * Priced before it runs: the count comes from the server, so the confirmation
   * says what the click actually costs rather than "this may take a while".
   */
  async function onAssign(tag: TagRecord) {
    try {
      setBusy(true)
      setError('')
      setNotice('')

      const preview = await previewTagAssign(tag.id)
      if (preview.candidates === 0) {
        setNotice(`Every document already has "${tag.name}", or none has text to judge.`)
        return
      }
      const asked = Math.min(preview.candidates, preview.limit)
      const capped =
        preview.candidates > preview.limit
          ? `\n\nOnly the ${preview.limit} newest of ${preview.candidates} run this time.`
          : ''
      if (
        !window.confirm(
          `Ask the model which of ${asked === 1 ? 'this document' : `these ${asked} documents`} warrant "${tag.name}"?\n\n` +
            `This sends ${asked === 1 ? 'one AI request' : `${asked} AI requests`} and is charged to your provider.\n` +
            'It only adds the tag; nothing else on the documents changes.' +
            capped,
        )
      ) {
        return
      }

      const result = await assignTagsWithAI([tag.id])
      setNotice(
        `Tagged ${result.assigned} of ${result.asked}; ${result.declined} did not warrant it` +
          (result.failed > 0 ? `, ${result.failed} failed` : '') +
          '.',
      )
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not assign the tag')
    } finally {
      setBusy(false)
    }
  }

  async function onDelete(tag: TagRecord) {
    if (
      !window.confirm(`Delete "${tag.name}"? It will be removed from every document that has it.`)
    ) {
      return
    }
    try {
      setBusy(true)
      setError('')
      setNotice('')
      await deleteTag(tag.id)
      await reload()
      setNotice(`Deleted "${tag.name}".`)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete the tag')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-5">
      <header>
        <h1 className="font-display text-2xl font-semibold text-ink">Tags</h1>
        <p className={`${fieldHintClassName} mt-1`}>
          Your tag vocabulary. Processing assigns tags from this list and never invents new ones, so
          a tag only exists once you create it here.
        </p>
        <p className={`${fieldHintClassName} mt-2`}>
          A new tag is not applied to documents already in the archive. "Find documents" searches for
          it so you can tag them yourself, for free. "Assign with AI" asks the model instead, one
          request per document, charged to your provider; it only ever adds the tag, but reprocessing
          a document later discards what it added.
        </p>
      </header>

      <section className={sectionClassName}>
        <h2 className={sectionTitleClassName}>Your tags</h2>

        {loadError && <p className="mb-3 text-sm text-madder">{loadError}</p>}
        {error && <p className="mb-3 text-sm text-madder">{error}</p>}
        {notice && <p className="mb-3 text-sm text-ink-soft">{notice}</p>}

        {loading ? (
          <p className="mb-4 text-sm text-ink-soft">Loading...</p>
        ) : rows.length === 0 ? (
          <p className="mb-4 text-sm text-ink-soft">
            No tags yet. Until you add one, documents are processed without any.
          </p>
        ) : (
          <ul className="mb-4 flex flex-col gap-2">
            {rows.map((tag) => (
              <TagRow
                key={tag.id}
                tag={tag}
                busy={busy}
                onRename={onRename}
                onDelete={onDelete}
                onAssign={onAssign}
              />
            ))}
          </ul>
        )}

        <form className="flex flex-col gap-3" onSubmit={onCreate}>
          <label className={labelClassName}>
            <span className={labelTextClassName}>New tag</span>
            <input
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder="Invoices"
              className={`${inputClassName} max-w-sm`}
            />
          </label>
          <p className={fieldHintClassName}>
            Keep them broad enough to reuse — a tag that fits one document is a title, not a tag.
          </p>
          <div>
            <Button type="submit" disabled={busy || !name.trim()}>
              Add tag
            </Button>
          </div>
        </form>
      </section>
    </div>
  )
}
