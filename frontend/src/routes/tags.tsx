import { type SubmitEvent, useState } from 'react'
import { Link } from '@tanstack/react-router'
import {
  MAX_TAG_ASSIGN_DOCUMENTS,
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
  ticked: boolean
  onTick: (id: string) => void
  onRename: (id: string, name: string) => Promise<void>
  onDelete: (tag: TagRecord) => Promise<void>
  onAssign: (tagIds: string[], label: string) => Promise<void>
}

function TagRow({ tag, busy, ticked, onTick, onRename, onDelete, onAssign }: TagRowProps) {
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
          <label className="flex min-w-0 flex-1 items-center gap-2">
            <input
              type="checkbox"
              checked={ticked}
              disabled={busy}
              onChange={() => onTick(tag.id)}
              aria-label={`Include ${tag.name}`}
            />
            <span className="min-w-0 truncate text-sm font-medium text-ink">{tag.name}</span>
          </label>
          <div className="flex flex-wrap items-center gap-2">
            <Link
              to="/"
              search={{ q: tag.name }}
              className="rounded-xs border border-line px-2 py-1 text-xs text-ink-soft transition-colors hover:text-ink"
            >
              Find documents
            </Link>
            <Button
              size="xs"
              variant="secondary"
              disabled={busy}
              onClick={() => void onAssign([tag.id], `"${tag.name}"`)}
            >
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
  const [ticked, setTicked] = useState<Set<string>>(new Set())

  const rows = tags ?? []
  // Ids the list still has: a tick can outlive the tag it was on.
  const tickedIds = rows.map((tag) => tag.id).filter((id) => ticked.has(id))

  function onTick(id: string) {
    setTicked((current) => {
      const next = new Set(current)
      if (next.has(id)) {
        next.delete(id)
      } else {
        next.add(id)
      }
      return next
    })
  }

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
   *
   * One call for however many tags. The prompt is mostly the document's own
   * text, so asking about five tags costs about what one costs, where five
   * separate runs would each re-read the archive.
   */
  async function onAssign(tagIds: string[], label: string) {
    try {
      setBusy(true)
      setError('')
      setNotice('')

      const preview = await previewTagAssign(tagIds)
      if (preview.candidates === 0) {
        setNotice(`Every document already carries ${label}, or none has text to judge.`)
        return
      }
      const asked = Math.min(preview.candidates, preview.limit)
      const capped =
        preview.candidates > preview.limit
          ? `\n\nOnly the ${preview.limit} newest of ${preview.candidates} run this time.`
          : ''
      if (
        !window.confirm(
          `Ask the model which of ${asked === 1 ? 'this document' : `these ${asked} documents`} warrant ${label}?\n\n` +
            `Your model reads ${asked === 1 ? 'the document' : `all ${asked} documents`}, and your provider charges for each one.\n` +
            'It only adds tags; nothing else on the documents changes.' +
            capped,
        )
      ) {
        return
      }

      const result = await assignTagsWithAI(tagIds)
      setNotice(
        `Tagged ${result.assigned} of ${result.asked}; ${result.declined} warranted nothing` +
          (result.failed > 0 ? `, ${result.failed} failed` : '') +
          '.',
      )
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not assign the tags')
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
          it, so you can open the ones that match and tag them from their own pages, for free.
        </p>
      </header>

      {/* Its own block rather than a line in the hints above: this is the only
          thing on the page that costs money, and the arithmetic decides which
          button a reader should press. */}
      <aside className="rounded-xs border-l-2 border-oxblood bg-bright px-4 py-3">
        <h2 className="text-sm font-semibold text-ink">What assigning with AI costs</h2>
        <ul className={`${fieldHintClassName} mt-2 flex list-disc flex-col gap-1 pl-4`}>
          <li>
            Your model reads every document that is missing the tags you ask about, and your provider
            charges for each one. Nothing runs until you press a button — creating a tag on its own
            costs nothing.
          </li>
          <li>
            <strong className="font-semibold text-ink">Tick several tags and run them together.</strong>{' '}
            A document is read once whatever it is being asked about, so one run over five tags costs
            about what one tag costs. Five separate runs re-read the archive five times.
          </li>
          <li>
            At most {MAX_TAG_ASSIGN_DOCUMENTS.toLocaleString()} documents per run, newest first. Run it
            again for the rest.
          </li>
          <li>
            Only tags are ever added — no title, date or correspondent changes. But reprocessing a
            document later replaces its tags wholesale and discards what was assigned here.
          </li>
        </ul>
      </aside>

      <section className={sectionClassName}>
        <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
          <h2 className={sectionTitleClassName}>Your tags</h2>
          {rows.length > 1 && (
            <div className="flex items-center gap-2">
              <Button
                size="xs"
                variant="secondary"
                disabled={busy || rows.length === 0}
                onClick={() =>
                  setTicked(
                    tickedIds.length === rows.length
                      ? new Set()
                      : new Set(rows.map((tag) => tag.id)),
                  )
                }
              >
                {tickedIds.length === rows.length ? 'Tick none' : 'Tick all'}
              </Button>
              <Button
                size="xs"
                disabled={busy || tickedIds.length === 0}
                onClick={() =>
                  void onAssign(
                    tickedIds,
                    tickedIds.length === 1
                      ? `"${rows.find((tag) => tag.id === tickedIds[0])?.name}"`
                      : `any of ${tickedIds.length} tags`,
                  )
                }
              >
                {busy ? 'Working...' : `Assign ${tickedIds.length || ''} ticked with AI`.trim()}
              </Button>
            </div>
          )}
        </div>

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
                ticked={ticked.has(tag.id)}
                onTick={onTick}
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
