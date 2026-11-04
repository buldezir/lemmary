import { useEffect, useRef, useState } from 'react'
import { useAsync } from '../hooks/useAsync'
import { t } from '../i18n'
import {
  CORRESPONDENT_FIELD_ID,
  DOCUMENT_TYPE_FIELD_ID,
  listCustomFields,
  type CustomFieldType,
} from '../lib/api/customFields'
import type { OptionRecord } from '../lib/api/documents'
import { Combobox } from './Combobox'
import { Button, inputClassName, labelClassName, labelTextClassName } from './ui'

type Field = { id: string; name: string; type: CustomFieldType | 'option'; values: string[] }

/** Mounted while open: each opening starts from an empty form. */
export function BulkFieldDialog({
  count,
  documentTypes,
  correspondents,
  busy,
  onApply,
  onClose,
}: {
  count: number
  documentTypes: OptionRecord[]
  correspondents: OptionRecord[]
  busy: boolean
  onApply: (fieldId: string, fieldName: string, value: string | number | null) => void
  onClose: () => void
}) {
  const ref = useRef<HTMLDialogElement>(null)
  const custom = useAsync(listCustomFields, [])
  const [fieldId, setFieldId] = useState('')
  const [value, setValue] = useState('')

  useEffect(() => {
    ref.current?.showModal()
  }, [])

  const fields: Field[] = [
    { id: DOCUMENT_TYPE_FIELD_ID, name: t('documentPage.fieldType'), type: 'option', values: documentTypes.map((o) => o.name) },
    { id: CORRESPONDENT_FIELD_ID, name: t('documentPage.fieldCorrespondent'), type: 'option', values: correspondents.map((o) => o.name) },
    ...(custom.data ?? []).map((f) => ({ ...f, values: f.choices.map((c) => c.name) })),
  ]
  const field = fields.find((f) => f.id === fieldId)
  const trimmed = value.trim()
  const applied = field?.type === 'number' ? Number(trimmed) : trimmed
  const valid = trimmed !== '' && (typeof applied === 'string' || Number.isFinite(applied))

  function pickField(id: string) {
    setFieldId(id)
    setValue('')
  }

  return (
    <dialog
      ref={ref}
      onClose={onClose}
      aria-labelledby="bulk-field-title"
      className="m-auto w-full max-w-md overflow-visible border border-line bg-surface p-5 text-ink backdrop:bg-ink/40"
    >
      <h2 id="bulk-field-title" className="font-display text-lg font-semibold text-ink">
        {t('bulk.setFieldTitle')}
      </h2>
      <p className="mt-1 text-sm text-ink-soft">{t('bulk.setFieldIntro', { count })}</p>

      <div className="mt-4 flex flex-col gap-3">
        <div className={labelClassName}>
          <label htmlFor="bulk-field" className={labelTextClassName}>
            {t('bulk.field')}
          </label>
          <Combobox
            id="bulk-field"
            value={fieldId}
            options={fields.map((f) => ({ value: f.id, label: f.name }))}
            placeholder={t('bulk.pickField')}
            bgClassName="bg-surface"
            loading={custom.loading}
            onChange={pickField}
          />
        </div>

        {field && (
          <div className={labelClassName}>
            <label htmlFor="bulk-field-value" className={labelTextClassName}>
              {t('bulk.value')}
            </label>
            {field.type === 'option' || field.type === 'choice' ? (
              <Combobox
                id="bulk-field-value"
                value={value}
                options={[...new Set([...field.values, ...(value ? [value] : [])])].map((name) => ({
                  value: name,
                  label: name,
                }))}
                placeholder={field.type === 'option' ? t('bulk.pickOrCreateValue') : t('bulk.pickValue')}
                bgClassName="bg-surface"
                onChange={setValue}
                onCreate={field.type === 'option' ? (name) => setValue(name.trim()) : undefined}
              />
            ) : (
              <input
                id="bulk-field-value"
                type={field.type}
                step={field.type === 'number' ? 'any' : undefined}
                className={inputClassName}
                value={value}
                onChange={(event) => setValue(event.target.value)}
              />
            )}
          </div>
        )}
        {custom.error && <p className="text-sm text-madder">{custom.error}</p>}
      </div>

      <div className="mt-5 flex flex-wrap justify-end gap-2">
        <Button variant="secondary" size="sm" onClick={onClose}>
          {t('common.cancel')}
        </Button>
        <Button
          variant="secondary"
          size="sm"
          disabled={busy || !field}
          onClick={() => field && onApply(field.id, field.name, null)}
        >
          {t('bulk.clearField')}
        </Button>
        <Button
          size="sm"
          disabled={busy || !field || !valid}
          onClick={() => field && onApply(field.id, field.name, applied)}
        >
          {t('bulk.apply')}
        </Button>
      </div>
    </dialog>
  )
}
