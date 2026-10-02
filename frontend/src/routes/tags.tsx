import { type SubmitEvent, useEffect, useRef, useState } from 'react'
import { Link } from '@tanstack/react-router'
import {
  MAX_TAG_ASSIGN_DOCUMENTS,
  assignTagsWithAI,
  createTag,
  deleteTag,
  listTags,
  previewTagAssign,
  renameTag,
  setTagColor,
  type TagRecord,
} from '../lib/api/tags'
import { useAsync } from '../hooks/useAsync'
import { exactSearch } from '../lib/documentQuery'
import {
  Button,
  fieldHintClassName,
  inputClassName,
  labelClassName,
  labelTextClassName,
  sectionClassName,
  sectionTitleClassName,
} from '../components/ui'
import { lang, t, tNode } from '../i18n'

/** What the picker opens on for a tag that has no colour of its own. */
const UNCOLORED_SWATCH = '#808080'

/**
 * React maps `onChange` on a colour input to the native `input` event, which
 * fires all through a drag inside the picker — one save per pixel. The native
 * `change` event fires once, when the picker closes, so the save hangs off that
 * instead and the input stays uncontrolled; the row remounts it by key when the
 * list reloads.
 */
function ColorSwatch({
  tag,
  busy,
  onPick,
}: {
  tag: TagRecord
  busy: boolean
  onPick: (id: string, color: string) => void
}) {
  const ref = useRef<HTMLInputElement>(null)

  useEffect(() => {
    const input = ref.current
    if (!input) {
      return
    }
    const commit = () => onPick(tag.id, input.value)
    input.addEventListener('change', commit)
    return () => input.removeEventListener('change', commit)
  }, [tag.id, onPick])

  return (
    <input
      ref={ref}
      type="color"
      aria-label={t('tagsPage.colorFor', { name: tag.name })}
      disabled={busy}
      defaultValue={tag.color || UNCOLORED_SWATCH}
      className="h-6 w-8 shrink-0 cursor-pointer border border-line bg-surface p-0.5 disabled:cursor-not-allowed disabled:opacity-50"
    />
  )
}

type TagRowProps = {
  tag: TagRecord
  busy: boolean
  ticked: boolean
  onTick: (id: string) => void
  onRename: (id: string, name: string) => Promise<void>
  onDelete: (tag: TagRecord) => Promise<void>
  onAssign: (tagIds: string[], label: string) => Promise<void>
  onColor: (id: string, color: string) => void
}

function TagRow({ tag, busy, ticked, onTick, onRename, onDelete, onAssign, onColor }: TagRowProps) {
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
    <li
      className="flex flex-wrap items-center justify-between gap-2 rounded-xs border border-line bg-bright px-3 py-2"
      style={tag.color ? { borderLeftColor: tag.color, borderLeftWidth: 3 } : undefined}
    >
      {editing ? (
        <form className="flex flex-1 flex-wrap items-center gap-2" onSubmit={onSubmit}>
          <input
            aria-label={t('tagsPage.tagName')}
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            className={`${inputClassName} max-w-xs flex-1`}
          />
          <Button type="submit" size="xs" disabled={busy}>
            {t('common.save')}
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
            {t('common.cancel')}
          </Button>
        </form>
      ) : (
        <>
          <div className="flex min-w-0 flex-1 items-center gap-2">
            <ColorSwatch key={tag.color ?? ''} tag={tag} busy={busy} onPick={onColor} />
            {tag.color && (
              <button
                type="button"
                aria-label={t('tagsPage.clearColorFor', { name: tag.name })}
                disabled={busy}
                className="shrink-0 text-xs text-ink-faint transition-colors hover:text-madder disabled:cursor-not-allowed disabled:opacity-50"
                onClick={() => onColor(tag.id, '')}
              >
                &times;
              </button>
            )}
            <label className="flex min-w-0 flex-1 items-center gap-2">
              <input
                type="checkbox"
                checked={ticked}
                disabled={busy}
                onChange={() => onTick(tag.id)}
                aria-label={t('tagsPage.include', { name: tag.name })}
              />
              <span className="min-w-0 truncate text-sm font-medium text-ink">{tag.name}</span>
            </label>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Link
              to="/"
              search={{ q: exactSearch(tag.name) }}
              className="rounded-xs border border-line px-2 py-1 text-xs text-ink-soft transition-colors hover:text-ink"
            >
              {t('tagsPage.findDocuments')}
            </Link>
            <Button
              size="xs"
              variant="secondary"
              disabled={busy}
              onClick={() => void onAssign([tag.id], t('tagsPage.quoted', { name: tag.name }))}
            >
              {t('tagsPage.assignWithAI')}
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
              {t('tagsPage.rename')}
            </Button>
            <Button size="xs" variant="danger" disabled={busy} onClick={() => void onDelete(tag)}>
              {t('common.delete')}
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
      setNotice(t('tagsPage.added', { name: next }))
    } catch (err) {
      setError(err instanceof Error ? err.message : t('tagsPage.createError'))
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
      setNotice(t('tagsPage.renamed', { name: nextName }))
    } catch (err) {
      setError(err instanceof Error ? err.message : t('tagsPage.renameError'))
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
        setNotice(t('tagsPage.nothingToAssign', { label }))
        return
      }
      const asked = Math.min(preview.candidates, preview.limit)
      const capped =
        preview.candidates > preview.limit
          ? '\n\n' + t('tagsPage.assignCapped', { limit: preview.limit, total: preview.candidates })
          : ''
      if (
        !window.confirm(
          t('tagsPage.confirmAssign', { count: asked, label }) +
            '\n\n' +
            t('tagsPage.confirmAssignCost', { count: asked }) +
            '\n' +
            t('tagsPage.confirmAssignNote') +
            capped,
        )
      ) {
        return
      }

      const result = await assignTagsWithAI(tagIds)
      setNotice(
        t(result.failed > 0 ? 'tagsPage.assignedWithFailures' : 'tagsPage.assigned', {
          assigned: result.assigned,
          asked: result.asked,
          declined: result.declined,
          failed: result.failed,
        }),
      )
    } catch (err) {
      setError(err instanceof Error ? err.message : t('tagsPage.assignError'))
    } finally {
      setBusy(false)
    }
  }

  async function onDelete(tag: TagRecord) {
    if (
      !window.confirm(t('tagsPage.confirmDelete', { name: tag.name }))
    ) {
      return
    }
    try {
      setBusy(true)
      setError('')
      setNotice('')
      await deleteTag(tag.id)
      await reload()
      setNotice(t('tagsPage.deleted', { name: tag.name }))
    } catch (err) {
      setError(err instanceof Error ? err.message : t('tagsPage.deleteError'))
    } finally {
      setBusy(false)
    }
  }

  async function onDeleteTicked() {
    const ids = tickedIds
    if (ids.length === 0) {
      return
    }
    if (
      !window.confirm(
        t('tagsPage.confirmDeleteTicked', { count: ids.length }) +
          '\n\n' +
          t('tagsPage.confirmDeleteTickedNote'),
      )
    ) {
      return
    }
    try {
      setBusy(true)
      setError('')
      setNotice('')
      await Promise.all(ids.map(deleteTag))
      setTicked(new Set())
      await reload()
      setNotice(t('tagsPage.deletedTicked', { count: ids.length }))
    } catch (err) {
      setError(err instanceof Error ? err.message : t('tagsPage.deleteTickedError'))
    } finally {
      setBusy(false)
    }
  }

  // No notice: the swatch and the row's edge are the confirmation.
  async function onColor(id: string, color: string) {
    try {
      setBusy(true)
      setError('')
      setNotice('')
      await setTagColor(id, color)
      await reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : t('tagsPage.colorError'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-5">
      <header>
        <h1 className="font-display text-2xl font-semibold text-ink">{t('tagsPage.title')}</h1>
        <p className={`${fieldHintClassName} mt-1`}>{t('tagsPage.intro')}</p>
        <p className={`${fieldHintClassName} mt-2`}>{t('tagsPage.introFind')}</p>
      </header>

      {/* Its own block rather than a line in the hints above: this is the only
          thing on the page that costs money, and the arithmetic decides which
          button a reader should press. */}
      <aside className="rounded-xs border-l-2 border-oxblood bg-bright px-4 py-3">
        <h2 className="text-sm font-semibold text-ink">{t('tagsPage.costTitle')}</h2>
        <ul className={`${fieldHintClassName} mt-2 flex list-disc flex-col gap-1 pl-4`}>
          <li>{t('tagsPage.costReads')}</li>
          <li>
            {tNode('tagsPage.costTogether', {
              strong: (
                <strong className="font-semibold text-ink">{t('tagsPage.costTogetherStrong')}</strong>
              ),
            })}
          </li>
          <li>{t('tagsPage.costLimit', { max: MAX_TAG_ASSIGN_DOCUMENTS.toLocaleString(lang) })}</li>
          <li>{t('tagsPage.costOnlyTags')}</li>
        </ul>
      </aside>

      <section className={sectionClassName}>
        <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
          <h2 className={sectionTitleClassName}>{t('tagsPage.yourTags')}</h2>
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
                {tickedIds.length === rows.length ? t('tagsPage.tickNone') : t('tagsPage.tickAll')}
              </Button>
              <Button
                size="xs"
                disabled={busy || tickedIds.length === 0}
                onClick={() =>
                  void onAssign(
                    tickedIds,
                    tickedIds.length === 1
                      ? t('tagsPage.quoted', {
                          name: rows.find((tag) => tag.id === tickedIds[0])?.name ?? '',
                        })
                      : t('tagsPage.anyOfTags', { count: tickedIds.length }),
                  )
                }
              >
                {busy
                  ? t('tagsPage.working')
                  : tickedIds.length
                    ? t('tagsPage.assignTicked', { count: tickedIds.length })
                    : t('tagsPage.assignTickedNone')}
              </Button>
              <Button
                size="xs"
                variant="danger"
                disabled={busy || tickedIds.length === 0}
                onClick={() => void onDeleteTicked()}
              >
                {t('tagsPage.deleteSelected')}
              </Button>
            </div>
          )}
        </div>

        {loadError && <p className="mb-3 text-sm text-madder">{loadError}</p>}
        {error && <p className="mb-3 text-sm text-madder">{error}</p>}
        {notice && <p className="mb-3 text-sm text-ink-soft">{notice}</p>}

        {loading ? (
          <p className="mb-4 text-sm text-ink-soft">{t('tagsPage.loading')}</p>
        ) : rows.length === 0 ? (
          <p className="mb-4 text-sm text-ink-soft">{t('tagsPage.empty')}</p>
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
                onColor={onColor}
              />
            ))}
          </ul>
        )}

        <form className="flex flex-col gap-3" onSubmit={onCreate}>
          <label className={labelClassName}>
            <span className={labelTextClassName}>{t('tagsPage.newTag')}</span>
            <input
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder={t('tagsPage.newTagPlaceholder')}
              className={`${inputClassName} max-w-sm`}
            />
          </label>
          <p className={fieldHintClassName}>{t('tagsPage.newTagHint')}</p>
          <div>
            <Button type="submit" disabled={busy || !name.trim()}>
              {t('tagsPage.addTag')}
            </Button>
          </div>
        </form>
      </section>
    </div>
  )
}
