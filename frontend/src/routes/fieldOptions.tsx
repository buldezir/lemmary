import { type SubmitEvent, useId, useState } from 'react'
import {
  createOption,
  deleteOption,
  listOptions,
  mergeOption,
  renameOption,
  type OptionRecord,
} from '../lib/api/documents'
import { listOptionFields, type OptionField } from '../lib/api/customFields'
import { useAsync } from '../hooks/useAsync'
import { Combobox } from '../components/Combobox'
import {
  Button,
  fieldHintClassName,
  inputClassName,
  labelClassName,
  labelTextClassName,
  sectionClassName,
  sectionTitleClassName,
} from '../components/ui'
import { t } from '../i18n'

type RowProps = {
  option: OptionRecord
  others: OptionRecord[]
  busy: boolean
  run: (work: () => Promise<string>) => Promise<boolean>
}

function OptionRow({ option, others, busy, run }: RowProps) {
  const id = useId()
  const [mode, setMode] = useState<'view' | 'rename' | 'merge'>('view')
  const [draft, setDraft] = useState(option.name)
  const [into, setInto] = useState('')

  async function onRename(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    const name = draft.trim()
    if (!name || name === option.name) {
      setMode('view')
      return
    }
    if (
      await run(async () => {
        await renameOption(option.id, name)
        return t('optionsPage.renamed', { name })
      })
    ) {
      setMode('view')
    }
  }

  async function onMerge(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    const target = others.find((other) => other.id === into)
    if (!target || !window.confirm(t('optionsPage.confirmMerge', { from: option.name, into: target.name }))) {
      return
    }
    await run(async () => {
      const count = await mergeOption(option.id, target.id)
      return t('optionsPage.merged', { from: option.name, into: target.name, count })
    })
  }

  async function onDelete() {
    if (!window.confirm(t('optionsPage.confirmDelete', { name: option.name }))) {
      return
    }
    await run(async () => {
      await deleteOption(option.id)
      return t('optionsPage.deleted', { name: option.name })
    })
  }

  const cancel = (
    <Button size="xs" variant="secondary" disabled={busy} onClick={() => setMode('view')}>
      {t('common.cancel')}
    </Button>
  )

  return (
    <li className="flex flex-wrap items-center justify-between gap-2 rounded-xs border border-line bg-bright px-3 py-2">
      {mode === 'rename' ? (
        <form className="flex flex-1 flex-wrap items-center gap-2" onSubmit={onRename}>
          <input
            aria-label={t('optionsPage.name')}
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            className={`${inputClassName} max-w-xs flex-1`}
          />
          <Button type="submit" size="xs" disabled={busy}>
            {t('common.save')}
          </Button>
          {cancel}
        </form>
      ) : mode === 'merge' ? (
        <form className="flex flex-1 flex-wrap items-center gap-2" onSubmit={onMerge}>
          <span className="text-sm text-ink">{t('optionsPage.mergeInto', { name: option.name })}</span>
          <Combobox
            id={`${id}-into`}
            ariaLabel={t('optionsPage.mergeTarget')}
            className="max-w-xs flex-1"
            value={into}
            placeholder={t('optionsPage.pickTarget')}
            options={others.map((other) => ({ value: other.id, label: other.name }))}
            onChange={setInto}
          />
          <Button type="submit" size="xs" disabled={busy || !into}>
            {t('optionsPage.merge')}
          </Button>
          {cancel}
        </form>
      ) : (
        <>
          <span className="min-w-0 flex-1 truncate text-sm font-medium text-ink">{option.name}</span>
          <div className="flex flex-wrap items-center gap-2">
            <Button
              size="xs"
              variant="secondary"
              disabled={busy}
              onClick={() => {
                setDraft(option.name)
                setMode('rename')
              }}
            >
              {t('optionsPage.rename')}
            </Button>
            <Button
              size="xs"
              variant="secondary"
              disabled={busy || others.length === 0}
              onClick={() => {
                setInto('')
                setMode('merge')
              }}
            >
              {t('optionsPage.merge')}
            </Button>
            <Button size="xs" variant="danger" disabled={busy} onClick={() => void onDelete()}>
              {t('common.delete')}
            </Button>
          </div>
        </>
      )}
    </li>
  )
}

function OptionSection({ field }: { field: OptionField }) {
  const headingId = useId()
  const { data, loading, error: loadError, reload } = useAsync(() => listOptions(field.id), [field.id])
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const rows = data ?? []

  async function run(work: () => Promise<string>): Promise<boolean> {
    try {
      setBusy(true)
      setError('')
      setNotice('')
      const done = await work()
      await reload()
      setNotice(done)
      return true
    } catch (err) {
      setError(err instanceof Error ? err.message : t('optionsPage.saveError'))
      return false
    } finally {
      setBusy(false)
    }
  }

  async function onCreate(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    const next = name.trim()
    if (
      next &&
      (await run(async () => {
        await createOption(field.id, next)
        return t('optionsPage.added', { name: next })
      }))
    ) {
      setName('')
    }
  }

  return (
    <section className={sectionClassName} aria-labelledby={headingId}>
      <h2 id={headingId} className={`${sectionTitleClassName} mb-3`}>
        {field.name}
      </h2>
      {loadError && <p className="mb-3 text-sm text-madder">{loadError}</p>}
      {error && <p className="mb-3 text-sm text-madder">{error}</p>}
      {notice && <p className="mb-3 text-sm text-ink-soft">{notice}</p>}
      {loading ? (
        <p className="mb-4 text-sm text-ink-soft">{t('optionsPage.loading')}</p>
      ) : rows.length === 0 ? (
        <p className="mb-4 text-sm text-ink-soft">{t('optionsPage.empty')}</p>
      ) : (
        <ul className="mb-4 flex flex-col gap-2">
          {rows.map((option) => (
            <OptionRow
              key={option.id}
              option={option}
              others={rows.filter((other) => other.id !== option.id)}
              busy={busy}
              run={run}
            />
          ))}
        </ul>
      )}
      <form className="flex flex-col gap-3" onSubmit={onCreate}>
        <label className={labelClassName}>
          <span className={labelTextClassName}>{t('optionsPage.new', { field: field.name })}</span>
          <input
            value={name}
            onChange={(event) => setName(event.target.value)}
            className={`${inputClassName} max-w-sm`}
          />
        </label>
        <div>
          <Button type="submit" disabled={busy || !name.trim()}>
            {t('optionsPage.add')}
          </Button>
        </div>
      </form>
    </section>
  )
}

export function FieldOptionsPage() {
  const { data: fields, loading, error } = useAsync(listOptionFields, [])
  return (
    <div className="flex flex-col gap-5">
      <header>
        <h1 className="font-display text-2xl font-semibold text-ink">{t('optionsPage.title')}</h1>
        <p className={`${fieldHintClassName} mt-1`}>{t('optionsPage.intro')}</p>
      </header>
      {error && <p className="text-sm text-madder">{error}</p>}
      {loading ? (
        <p className="text-sm text-ink-soft">{t('optionsPage.loading')}</p>
      ) : (
        (fields ?? []).map((field) => <OptionSection key={field.id} field={field} />)
      )}
    </div>
  )
}
