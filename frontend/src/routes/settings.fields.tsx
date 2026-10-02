import { type KeyboardEvent, type SubmitEvent, useEffect, useRef, useState } from 'react'

import { useAsync } from '../hooks/useAsync'
import {
  Button,
  fieldHintClassName,
  inputClassName,
  labelClassName,
  labelTextClassName,
  sectionClassName,
} from '../components/ui'
import {
  CUSTOM_FIELD_TYPES,
  deleteCustomField,
  listCustomFields,
  saveCustomField,
  type CustomField,
  type CustomFieldInput,
  type CustomFieldType,
} from '../lib/api/customFields'
import { t } from '../i18n'

const TYPE_LABELS: Record<CustomFieldType, string> = {
  text: t('settingsFields.typeText'),
  number: t('settingsFields.typeNumber'),
  date: t('settingsFields.typeDate'),
  choice: t('settingsFields.typeChoice'),
}

const NEW_FIELD: CustomField = { id: '', name: '', type: 'text', description: '', choices: [] }

const chipClassName = 'rounded-xs border border-line-strong bg-wash px-2 py-0.5 text-xs text-ink'

const squareButtonClassName =
  'flex h-9 w-9 shrink-0 items-center justify-center rounded-xs text-lg leading-none transition-colors'

/** The admin's document fields: read here, changed one at a time in a dialog. */
export function SettingsFieldsPage() {
  const { data: fields, loading, error: loadError, reload } = useAsync(listCustomFields, [])
  // null while the dialog is closed; a field without an id is a new one.
  const [editing, setEditing] = useState<CustomField | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  async function remove(field: CustomField) {
    if (!window.confirm(t('settingsFields.removeConfirm', { name: field.name }))) return
    setBusy(true)
    setError('')
    try {
      await deleteCustomField(field.id)
      await reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : t('customFieldsApi.saveFailed'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className={sectionClassName}>
      <div className="mb-3 flex items-center justify-between gap-3 border-b border-line pb-2">
        <h2 className="font-display text-lg font-semibold text-ink">{t('settingsFields.title')}</h2>
        <Button size="sm" onClick={() => setEditing(NEW_FIELD)}>
          {t('settingsFields.add')}
        </Button>
      </div>
      <p className={fieldHintClassName}>{t('settingsFields.intro')}</p>
      {(loadError || error) && <p className="mt-3 text-sm text-madder">{loadError || error}</p>}

      {loading ? (
        <p className="mt-4 text-sm text-ink-soft">{t('common.loading')}</p>
      ) : (fields ?? []).length === 0 ? (
        <p className="mt-4 text-sm text-ink-soft">{t('settingsFields.empty')}</p>
      ) : (
        <ul className="mt-4 divide-y divide-line border border-line">
          {(fields ?? []).map((field) => (
            <li key={field.id} className="flex items-start justify-between gap-3 bg-bright px-3 py-2.5">
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-baseline gap-x-2">
                  <span className="text-sm font-medium text-ink">{field.name}</span>
                  <span className={labelTextClassName}>{TYPE_LABELS[field.type]}</span>
                </div>
                {field.description && (
                  <p className={`${fieldHintClassName} mt-0.5 line-clamp-2`}>{field.description}</p>
                )}
                {field.type === 'choice' && (
                  <ul className="mt-1.5 flex flex-wrap gap-1">
                    {field.choices.map((choice) => (
                      <li key={choice.id} className={chipClassName}>
                        {choice.name}
                      </li>
                    ))}
                  </ul>
                )}
              </div>
              <div className="flex shrink-0 gap-2">
                <Button size="xs" variant="secondary" disabled={busy} onClick={() => setEditing(field)}>
                  {t('settingsFields.edit')}
                </Button>
                <Button size="xs" variant="danger" disabled={busy} onClick={() => void remove(field)}>
                  {t('common.delete')}
                </Button>
              </div>
            </li>
          ))}
        </ul>
      )}

      <FieldDialog
        field={editing}
        onClose={() => setEditing(null)}
        onSaved={async () => {
          setEditing(null)
          await reload()
        }}
      />
    </section>
  )
}

function FieldDialog({
  field,
  onClose,
  onSaved,
}: {
  field: CustomField | null
  onClose: () => void
  onSaved: () => Promise<void>
}) {
  const ref = useRef<HTMLDialogElement>(null)
  const [draft, setDraft] = useState<CustomFieldInput>(NEW_FIELD)
  const [newChoice, setNewChoice] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  // Every opening starts from the field as saved.
  const [openedFor, setOpenedFor] = useState<CustomField | null>(null)
  if (openedFor !== field) {
    setOpenedFor(field)
    if (field) {
      setDraft(field)
      setNewChoice('')
      setError('')
    }
  }

  useEffect(() => {
    const dialog = ref.current
    if (!dialog) return
    if (!field) dialog.close()
    else if (!dialog.open) dialog.showModal()
  }, [field])

  const isNew = !field?.id
  const isChoice = draft.type === 'choice'
  const pending = newChoice.trim()

  function setChoices(choices: CustomFieldInput['choices']) {
    setDraft({ ...draft, choices })
  }

  function addChoice() {
    if (!pending) return
    if (draft.choices.some((c) => c.name.trim().toLowerCase() === pending.toLowerCase())) {
      setError(t('customFieldsApi.duplicateChoice', { name: pending }))
      return
    }
    setError('')
    setChoices([...draft.choices, { id: '', name: pending }])
    setNewChoice('')
  }

  function onNewChoiceKey(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key === 'Enter') {
      event.preventDefault()
      addChoice()
    }
  }

  async function onSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!field) return
    // A choice typed but not yet added is meant to be saved with the rest.
    const input = isChoice && pending
      ? { ...draft, choices: [...draft.choices, { id: '', name: pending }] }
      : draft
    const removed = isChoice
      ? field.choices.filter((c) => !input.choices.some((d) => d.id === c.id))
      : []
    if (
      removed.length > 0 &&
      !window.confirm(
        t('settingsFields.choicesRemoveConfirm', { names: removed.map((c) => c.name).join(', ') }),
      )
    ) {
      return
    }
    setSaving(true)
    setError('')
    try {
      await saveCustomField(field.id, input)
      await onSaved()
    } catch (err) {
      setError(err instanceof Error ? err.message : t('customFieldsApi.saveFailed'))
    } finally {
      setSaving(false)
    }
  }

  return (
    <dialog
      ref={ref}
      onClose={onClose}
      aria-labelledby="field-dialog-title"
      className="m-auto w-full max-w-lg border border-line bg-surface p-5 text-ink backdrop:bg-ink/40"
    >
      <form onSubmit={onSubmit} className="flex flex-col gap-4">
        <h2 id="field-dialog-title" className="font-display text-lg font-semibold text-ink">
          {isNew ? t('settingsFields.addTitle') : t('settingsFields.editTitle')}
        </h2>

        <label className={labelClassName}>
          <span className={labelTextClassName}>{t('settingsFields.name')}</span>
          <input
            className={inputClassName}
            required
            maxLength={100}
            placeholder={t('settingsFields.namePlaceholder')}
            value={draft.name}
            onChange={(e) => setDraft({ ...draft, name: e.target.value })}
          />
        </label>

        {isNew ? (
          <fieldset className="flex flex-col gap-1.5">
            <legend className={`${labelTextClassName} mb-1`}>{t('settingsFields.type')}</legend>
            <div className="flex flex-wrap gap-2">
              {CUSTOM_FIELD_TYPES.map((type) => (
                <label
                  key={type}
                  className="flex items-center gap-1.5 rounded-xs border border-line-strong bg-bright px-3 py-1.5 text-sm text-ink has-checked:border-oxblood"
                >
                  <input
                    type="radio"
                    name="field-type"
                    className="accent-oxblood"
                    value={type}
                    checked={draft.type === type}
                    onChange={() => setDraft({ ...draft, type })}
                  />
                  {TYPE_LABELS[type]}
                </label>
              ))}
            </div>
            <p className={fieldHintClassName}>{t('settingsFields.typeFixed')}</p>
          </fieldset>
        ) : (
          <div className="flex flex-col gap-1">
            <span className={labelTextClassName}>{t('settingsFields.type')}</span>
            <p className="text-sm text-ink">
              {TYPE_LABELS[draft.type]}
              <span className={`${fieldHintClassName} ml-2`}>{t('settingsFields.typeLocked')}</span>
            </p>
          </div>
        )}

        <div className={labelClassName}>
          <label className={labelClassName}>
            <span className={labelTextClassName}>{t('settingsFields.hint')}</span>
            <textarea
              rows={2}
              className={inputClassName}
              maxLength={500}
              placeholder={t('settingsFields.hintPlaceholder')}
              value={draft.description}
              onChange={(e) => setDraft({ ...draft, description: e.target.value })}
            />
          </label>
          <p className={fieldHintClassName}>{t('settingsFields.hintHelp')}</p>
        </div>

        {isChoice && (
          <div className={labelClassName}>
            <span className={labelTextClassName}>{t('settingsFields.choices')}</span>
            <ul className="flex flex-col gap-1.5">
              {draft.choices.map((choice, index) => (
                <li key={choice.id || `new-${index}`} className="flex items-center gap-2">
                  <input
                    className={inputClassName}
                    aria-label={t('settingsFields.choiceN', { n: index + 1 })}
                    required
                    maxLength={500}
                    value={choice.name}
                    onChange={(e) =>
                      setChoices(
                        draft.choices.map((c, i) => (i === index ? { ...c, name: e.target.value } : c)),
                      )
                    }
                  />
                  <button
                    type="button"
                    aria-label={t('settingsFields.removeChoice', { name: choice.name })}
                    title={t('settingsFields.removeChoice', { name: choice.name })}
                    onClick={() => setChoices(draft.choices.filter((_, i) => i !== index))}
                    className={`${squareButtonClassName} text-ink-faint hover:text-madder`}
                  >
                    &times;
                  </button>
                </li>
              ))}
              <li className="flex items-center gap-2">
                <input
                  className={inputClassName}
                  aria-label={t('settingsFields.newChoice')}
                  placeholder={t('settingsFields.newChoicePlaceholder')}
                  maxLength={500}
                  value={newChoice}
                  onChange={(e) => setNewChoice(e.target.value)}
                  onKeyDown={onNewChoiceKey}
                />
                <button
                  type="button"
                  aria-label={t('settingsFields.addChoice')}
                  title={t('settingsFields.addChoice')}
                  disabled={!pending}
                  onClick={addChoice}
                  className={`${squareButtonClassName} border border-line-strong text-ink hover:border-ink disabled:opacity-40`}
                >
                  +
                </button>
              </li>
            </ul>
            <p className={fieldHintClassName}>{t('settingsFields.choicesHelp')}</p>
          </div>
        )}

        {error && <p className="text-sm text-madder">{error}</p>}

        <div className="flex justify-end gap-2">
          <Button variant="secondary" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button
            type="submit"
            disabled={
              saving ||
              !draft.name.trim() ||
              (isChoice &&
                ((draft.choices.length === 0 && !pending) || draft.choices.some((c) => !c.name.trim())))
            }
          >
            {saving ? t('settingsFields.saving') : t('common.save')}
          </Button>
        </div>
      </form>
    </dialog>
  )
}
