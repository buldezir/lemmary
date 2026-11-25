import { type ReactNode, type SubmitEvent, useEffect, useId, useRef, useState } from 'react'

import { useAsync } from '../hooks/useAsync'
import { Combobox, type ComboboxOption } from '../components/Combobox'
import {
  Button,
  DocsLink,
  fieldHintClassName,
  inputClassName,
  labelClassName,
  labelTextClassName,
  sectionClassName,
  sectionTitleClassName,
} from '../components/ui'
import {
  CORRESPONDENT_FIELD_ID,
  DOCUMENT_TYPE_FIELD_ID,
  listCustomFields,
  type CustomField,
} from '../lib/api/customFields'
import { listOptions, type OptionRecord } from '../lib/api/documents'
import { listTags, type TagRecord } from '../lib/api/tags'
import {
  deleteRule,
  listRules,
  reorderRules,
  saveRule,
  setRuleEnabled,
  type RuleRecord,
} from '../lib/api/rules'
import {
  ACTION_LABELS,
  ACTION_TYPES,
  OPS_BY_KIND,
  RULE_TRIGGERS,
  SUBJECT_AI,
  SUBJECT_CONFIDENCE,
  SUBJECT_TAG,
  SUBJECT_TEXT,
  SUBJECT_TITLE,
  TRIGGER_LABELS,
  conditionFor,
  defaultAction,
  needsValue,
  opLabel,
  ruleActionsForSave,
  ruleConditionsForSave,
  subjectKind,
  withSubject,
  type RuleAction,
  type RuleActionType,
  type RuleCondition,
  type RuleOp,
  type RuleTrigger,
  type SubjectKind,
} from '../lib/rules'
import { docsUrl, t } from '../i18n'

/** What a rule's ids point at, with names: loaded once for the list and the editor. */
type Catalog = {
  tags: TagRecord[]
  fields: CustomField[]
  options: Record<string, OptionRecord[]>
  /**
   * A global rule's: it runs on every user's documents, and tags, types and
   * correspondents are each user's own, so it names them instead of pointing
   * at the admin's. The admin's own lists are only suggestions.
   */
  named: boolean
}

type Draft = {
  name: string
  enabled: boolean
  triggers: RuleTrigger[]
  conditions: RuleCondition[]
  actions: RuleAction[]
}

const NEW_DRAFT: Draft = {
  name: '',
  enabled: true,
  triggers: ['processed'],
  conditions: [],
  actions: [defaultAction('add_tag')],
}

// A row's pickers keep a fixed width beside each other and its value takes the
// rest; on a phone each takes a line of its own.
const pickerClassName = 'w-full sm:w-52 sm:shrink-0'
const valueClassName = 'min-w-0 basis-full sm:min-w-40 sm:flex-1 sm:basis-0'

const removeButtonClassName =
  'flex h-9 w-9 shrink-0 items-center justify-center rounded-xs text-lg leading-none text-ink-faint transition-colors hover:text-madder'

async function loadPage(global: boolean): Promise<{ rules: RuleRecord[]; catalog: Catalog }> {
  const [rules, tags, fields] = await Promise.all([listRules(global), listTags(), listCustomFields()])
  const optionFieldIds = [
    DOCUMENT_TYPE_FIELD_ID,
    CORRESPONDENT_FIELD_ID,
    ...fields.filter((field) => field.type === 'option').map((field) => field.id),
  ]
  const lists = await Promise.all(optionFieldIds.map((id) => listOptions(id)))
  return {
    rules,
    catalog: {
      tags,
      fields,
      options: Object.fromEntries(optionFieldIds.map((id, i) => [id, lists[i]])),
      named: global,
    },
  }
}

function subjectName(subject: string, catalog: Catalog): string {
  switch (subject) {
    case SUBJECT_TITLE:
      return t('rules.subjectTitle')
    case SUBJECT_TEXT:
      return t('rules.subjectText')
    case SUBJECT_TAG:
      return t('rules.subjectTag')
    case SUBJECT_CONFIDENCE:
      return t('rules.subjectConfidence')
    case SUBJECT_AI:
      return t('rules.subjectAI')
    case DOCUMENT_TYPE_FIELD_ID:
      return t('rules.subjectDocumentType')
    case CORRESPONDENT_FIELD_ID:
      return t('rules.subjectCorrespondent')
  }
  return catalog.fields.find((f) => f.id === subject)?.name ?? t('rules.missing')
}

/** A stored value as its owner reads it: an id becomes its name. */
function valueName(subject: string, kind: SubjectKind | null, value: unknown, catalog: Catalog): string {
  const id = String(value ?? '')
  if (catalog.named && (kind === 'tag' || kind === 'option')) return id
  if (kind === 'tag') return catalog.tags.find((tag) => tag.id === id)?.name ?? t('rules.missing')
  if (kind === 'option') return catalog.options[subject]?.find((o) => o.id === id)?.name ?? t('rules.missing')
  if (kind === 'choice') {
    const field = catalog.fields.find((f) => f.id === subject)
    return field?.choices.find((c) => c.id === id)?.name ?? t('rules.missing')
  }
  return id
}

function describeCondition(c: RuleCondition, catalog: Catalog): string {
  const kind = subjectKind(c.subject, catalog.fields)
  const words = [subjectName(c.subject, catalog), kind ? opLabel(c.op, kind) : c.op]
  if (needsValue(c.op)) words.push(`"${valueName(c.subject, kind, c.value, catalog)}"`)
  return words.join(' ')
}

function describeAction(a: RuleAction, catalog: Catalog): string {
  switch (a.type) {
    case 'add_tag':
    case 'remove_tag':
      return `${ACTION_LABELS[a.type]} "${valueName('', 'tag', a.value, catalog)}"`
    case 'set_field': {
      const field = a.field ?? ''
      const value = valueName(field, subjectKind(field, catalog.fields), a.value, catalog)
      return `${ACTION_LABELS[a.type]} ${subjectName(field, catalog)} = "${value}"`
    }
    case 'ai_extract':
      return `${ACTION_LABELS[a.type]}: ${(a.fields ?? []).map((id) => subjectName(id, catalog)).join(', ')}`
    case 'webhook': {
      let host = a.url ?? ''
      try {
        host = new URL(host).host
      } catch {
        // Shown as typed.
      }
      return `${ACTION_LABELS[a.type]} ${host}`
    }
  }
  return ACTION_LABELS[a.type]
}

/** The owner's if-then rules: listed and ordered here, each edited in a dialog. */
export function RulesPage() {
  return (
    <div className="flex flex-col gap-5">
      <header>
        <h1 className="font-display text-2xl font-semibold text-ink">{t('rulesPage.title')}</h1>
        <p className={`${fieldHintClassName} mt-1`}>{t('rulesPage.intro')}</p>
        <p className={`${fieldHintClassName} mt-2`}>
          {t('rulesPage.order')}{' '}
          <DocsLink href={docsUrl('rules')}>{t('rulesPage.docs')}</DocsLink>
        </p>
      </header>
      <RulesSection global={false} />
    </div>
  )
}

/** The admin's global rules, which run on every user's documents before their own. */
export function SettingsRulesPage() {
  return <RulesSection global />
}

function RulesSection({ global }: { global: boolean }) {
  const { data, loading, error: loadError, reload } = useAsync(() => loadPage(global), [global])
  // null while the dialog is closed; a rule without an id is a new one.
  const [editing, setEditing] = useState<RuleRecord | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const rules = data?.rules ?? []
  const catalog = data?.catalog

  async function run(action: () => Promise<void>) {
    setBusy(true)
    setError('')
    try {
      await action()
      await reload()
    } catch (err) {
      setError(err instanceof Error ? err.message : t('rulesApi.saveFailed'))
    } finally {
      setBusy(false)
    }
  }

  function move(index: number, by: number) {
    const ordered = [...rules]
    const [rule] = ordered.splice(index, 1)
    ordered.splice(index + by, 0, rule)
    void run(() => reorderRules(ordered))
  }

  function remove(rule: RuleRecord) {
    if (!window.confirm(t('rulesPage.deleteConfirm', { name: rule.name }))) return
    void run(() => deleteRule(rule.id))
  }

  return (
    <>
      <section className={sectionClassName}>
        <div className="mb-3 flex items-center justify-between gap-3">
          <h2 className={`${sectionTitleClassName} mb-0 flex-1`}>
            {global ? t('rulesPage.globalTitle') : t('rulesPage.yourRules')}
          </h2>
          <Button size="sm" disabled={!catalog} onClick={() =>
              setEditing({ ...NEW_DRAFT, id: '', user: '', position: Math.max(-1, ...rules.map((r) => r.position)) + 1 })
            }
          >
            {t('rulesPage.add')}
          </Button>
        </div>
        {global && (
          <p className={`${fieldHintClassName} mb-3`}>
            {t('rulesPage.globalIntro')}{' '}
            <DocsLink href={docsUrl('rules#global')}>{t('rulesPage.docs')}</DocsLink>
          </p>
        )}
        {(loadError || error) && <p className="mb-3 text-sm text-madder">{loadError || error}</p>}

        {loading ? (
          <p className="text-sm text-ink-soft">{t('common.loading')}</p>
        ) : !catalog || rules.length === 0 ? (
          <p className="text-sm text-ink-soft">{t('rulesPage.empty')}</p>
        ) : (
          <ol className="flex flex-col gap-2">
            {rules.map((rule, index) => (
              <li
                key={rule.id}
                aria-label={rule.name}
                className={`flex flex-wrap items-start justify-between gap-3 rounded-xs border border-line bg-bright px-3 py-2.5 ${rule.enabled ? '' : 'opacity-60'}`}
              >
                <div className="min-w-0 basis-full sm:flex-1 sm:basis-0">
                  <div className="flex flex-wrap items-baseline gap-x-2">
                    <span className="text-sm font-medium text-ink">{rule.name}</span>
                    <span className={labelTextClassName}>
                      {rule.triggers.map((trigger) => TRIGGER_LABELS[trigger]).join(' · ')}
                    </span>
                  </div>
                  <p className={`${fieldHintClassName} mt-0.5`}>
                    {(rule.conditions ?? []).length === 0
                      ? t('rulesPage.always')
                      : `${t('rulesPage.if')} ${(rule.conditions ?? []).map((c) => describeCondition(c, catalog)).join(` ${t('rulesPage.and')} `)}`}
                  </p>
                  <p className={`${fieldHintClassName} mt-0.5`}>
                    → {(rule.actions ?? []).map((a) => describeAction(a, catalog)).join('; ')}
                  </p>
                </div>
                <div className="flex shrink-0 flex-wrap items-center gap-2">
                  <label className="flex items-center gap-1.5 text-xs text-ink">
                    <input
                      type="checkbox"
                      className="accent-oxblood"
                      checked={rule.enabled}
                      disabled={busy}
                      onChange={(e) => void run(() => setRuleEnabled(rule.id, e.target.checked))}
                    />
                    {t('rulesPage.enabled')}
                  </label>
                  <Button
                    size="xs"
                    variant="secondary"
                    aria-label={t('rulesPage.moveUp', { name: rule.name })}
                    disabled={busy || index === 0}
                    onClick={() => move(index, -1)}
                  >
                    ↑
                  </Button>
                  <Button
                    size="xs"
                    variant="secondary"
                    aria-label={t('rulesPage.moveDown', { name: rule.name })}
                    disabled={busy || index === rules.length - 1}
                    onClick={() => move(index, 1)}
                  >
                    ↓
                  </Button>
                  <Button size="xs" variant="secondary" disabled={busy} onClick={() => setEditing(rule)}>
                    {t('rulesPage.edit')}
                  </Button>
                  <Button size="xs" variant="danger" disabled={busy} onClick={() => remove(rule)}>
                    {t('common.delete')}
                  </Button>
                </div>
              </li>
            ))}
          </ol>
        )}
      </section>

      {catalog && (
        <RuleDialog
          rule={editing}
          catalog={catalog}
          onClose={() => setEditing(null)}
          onSaved={async () => {
            setEditing(null)
            await reload()
          }}
        />
      )}
    </>
  )
}

function RuleDialog({
  rule,
  catalog,
  onClose,
  onSaved,
}: {
  rule: RuleRecord | null
  catalog: Catalog
  onClose: () => void
  onSaved: () => Promise<void>
}) {
  const ref = useRef<HTMLDialogElement>(null)
  const [draft, setDraft] = useState<Draft>(NEW_DRAFT)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  // Every opening starts from the rule as saved.
  const [openedFor, setOpenedFor] = useState<RuleRecord | null>(null)
  if (openedFor !== rule) {
    setOpenedFor(rule)
    if (rule) {
      setDraft({
        name: rule.name,
        enabled: rule.enabled,
        triggers: rule.triggers,
        conditions: rule.conditions ?? [],
        actions: rule.actions ?? [],
      })
      setError('')
    }
  }

  useEffect(() => {
    const dialog = ref.current
    if (!dialog) return
    if (!rule) dialog.close()
    else if (!dialog.open) dialog.showModal()
  }, [rule])

  function toggleTrigger(trigger: RuleTrigger, on: boolean) {
    const triggers = on ? [...draft.triggers, trigger] : draft.triggers.filter((x) => x !== trigger)
    setDraft({ ...draft, triggers: RULE_TRIGGERS.filter((x) => triggers.includes(x)) })
  }

  function setCondition(index: number, condition: RuleCondition) {
    setDraft({ ...draft, conditions: draft.conditions.map((c, i) => (i === index ? condition : c)) })
  }

  function setAction(index: number, action: RuleAction) {
    setDraft({ ...draft, actions: draft.actions.map((a, i) => (i === index ? action : a)) })
  }

  async function onSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!rule) return
    setSaving(true)
    setError('')
    try {
      await saveRule(
        rule.id,
        {
          name: draft.name,
          enabled: draft.enabled,
          triggers: draft.triggers,
          conditions: ruleConditionsForSave(draft.conditions, catalog.fields),
          actions: ruleActionsForSave(draft.actions, catalog.fields),
        },
        rule.position,
        catalog.named,
      )
      await onSaved()
    } catch (err) {
      setError(err instanceof Error ? err.message : t('rulesApi.saveFailed'))
    } finally {
      setSaving(false)
    }
  }

  return (
    <dialog
      ref={ref}
      onClose={onClose}
      aria-labelledby="rule-dialog-title"
      className="m-auto w-[calc(100%-1rem)] max-w-4xl border border-line bg-surface p-4 text-ink backdrop:bg-ink/40 sm:p-5"
    >
      <form onSubmit={onSubmit} className="flex flex-col gap-5">
        <h2 id="rule-dialog-title" className="font-display text-lg font-semibold text-ink">
          {rule?.id ? t('rulesPage.editTitle') : t('rulesPage.addTitle')}
        </h2>

        <label className={labelClassName}>
          <span className={labelTextClassName}>{t('rulesPage.name')}</span>
          <input
            className={inputClassName}
            required
            maxLength={100}
            placeholder={t('rulesPage.namePlaceholder')}
            value={draft.name}
            onChange={(e) => setDraft({ ...draft, name: e.target.value })}
          />
        </label>

        <fieldset className="flex flex-col gap-1.5">
          <legend className={`${labelTextClassName} mb-1`}>{t('rulesPage.when')}</legend>
          {RULE_TRIGGERS.map((trigger) => (
            <label key={trigger} className="flex items-center gap-2 text-sm text-ink">
              <input
                type="checkbox"
                className="accent-oxblood"
                checked={draft.triggers.includes(trigger)}
                onChange={(e) => toggleTrigger(trigger, e.target.checked)}
              />
              {TRIGGER_LABELS[trigger]}
            </label>
          ))}
        </fieldset>

        <fieldset className="flex flex-col gap-2">
          <legend className={`${labelTextClassName} mb-1`}>{t('rulesPage.conditions')}</legend>
          {draft.conditions.length === 0 && <p className={fieldHintClassName}>{t('rulesPage.noConditions')}</p>}
          {draft.conditions.map((condition, index) => (
            <ConditionRow
              key={index}
              index={index}
              condition={condition}
              catalog={catalog}
              onChange={(c) => setCondition(index, c)}
              onRemove={() => setDraft({ ...draft, conditions: draft.conditions.filter((_, i) => i !== index) })}
            />
          ))}
          <div>
            <Button
              size="xs"
              variant="secondary"
              onClick={() =>
                setDraft({ ...draft, conditions: [...draft.conditions, conditionFor(DOCUMENT_TYPE_FIELD_ID, catalog.fields)] })
              }
            >
              {t('rulesPage.addCondition')}
            </Button>
          </div>
          {draft.conditions.some((c) => c.subject === SUBJECT_AI) && (
            <p className={fieldHintClassName}>{t('rulesPage.aiQuestionHint')}</p>
          )}
        </fieldset>

        <fieldset className="flex flex-col gap-2">
          <legend className={`${labelTextClassName} mb-1`}>{t('rulesPage.actions')}</legend>
          {draft.actions.map((action, index) => (
            <ActionRow
              key={index}
              index={index}
              action={action}
              catalog={catalog}
              onChange={(a) => setAction(index, a)}
              onRemove={() => setDraft({ ...draft, actions: draft.actions.filter((_, i) => i !== index) })}
            />
          ))}
          <div>
            <Button
              size="xs"
              variant="secondary"
              onClick={() => setDraft({ ...draft, actions: [...draft.actions, defaultAction('add_tag')] })}
            >
              {t('rulesPage.addAction')}
            </Button>
          </div>
          <p className={fieldHintClassName}>{t('rulesPage.actionsHint')}</p>
        </fieldset>

        <label className="flex items-center gap-2 text-sm text-ink">
          <input
            type="checkbox"
            className="accent-oxblood"
            checked={draft.enabled}
            onChange={(e) => setDraft({ ...draft, enabled: e.target.checked })}
          />
          {t('rulesPage.enabled')}
        </label>

        {error && <p className="text-sm text-madder">{error}</p>}

        <div className="flex justify-end gap-2">
          <Button variant="secondary" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button
            type="submit"
            disabled={saving || !draft.name.trim() || draft.triggers.length === 0 || draft.actions.length === 0}
          >
            {saving ? t('rulesPage.saving') : t('common.save')}
          </Button>
        </div>
      </form>
    </dialog>
  )
}

function RowShell({
  label,
  onRemove,
  children,
}: {
  label: string
  onRemove: () => void
  children: ReactNode
}) {
  return (
    <div role="group" aria-label={label} className="flex items-start gap-2 border border-line bg-bright p-2">
      <div className="flex min-w-0 flex-1 flex-wrap items-start gap-2">{children}</div>
      <button type="button" aria-label={t('rulesPage.remove', { what: label })} title={t('rulesPage.remove', { what: label })} onClick={onRemove} className={removeButtonClassName}>
        &times;
      </button>
    </div>
  )
}

/**
 * The fields a condition reads or an action sets, plus the one a rule already
 * names when it was deleted since, so the control still shows something.
 */
function subjectOptions(catalog: Catalog, withMeta: boolean, current: string): ComboboxOption[] {
  const ids = [
    DOCUMENT_TYPE_FIELD_ID,
    CORRESPONDENT_FIELD_ID,
    ...(withMeta ? [SUBJECT_TAG, SUBJECT_TITLE, SUBJECT_TEXT, SUBJECT_CONFIDENCE, SUBJECT_AI] : []),
    ...catalog.fields.map((field) => field.id),
  ]
  const options = ids.map((id) => ({ value: id, label: subjectName(id, catalog) }))
  return current && !ids.includes(current) ? [{ value: current, label: t('rules.missing') }, ...options] : options
}

function ConditionRow({
  index,
  condition,
  catalog,
  onChange,
  onRemove,
}: {
  index: number
  condition: RuleCondition
  catalog: Catalog
  onChange: (c: RuleCondition) => void
  onRemove: () => void
}) {
  const id = useId()
  const kind = subjectKind(condition.subject, catalog.fields)
  const label = t('rulesPage.conditionN', { n: index + 1 })
  return (
    <RowShell label={label} onRemove={onRemove}>
      <Combobox
        id={`${id}-subject`}
        ariaLabel={t('rulesPage.subject')}
        className={pickerClassName}
        value={condition.subject}
        options={subjectOptions(catalog, true, condition.subject)}
        onChange={(subject) => onChange(withSubject(condition, subject, catalog.fields))}
      />
      {kind && (
        <Combobox
          id={`${id}-op`}
          ariaLabel={t('rulesPage.comparison')}
          className={pickerClassName}
          value={condition.op}
          options={OPS_BY_KIND[kind].map((op) => ({ value: op, label: opLabel(op, kind) }))}
          onChange={(op) => onChange({ ...condition, op: op as RuleOp })}
        />
      )}
      {kind && needsValue(condition.op) && (
        <ValueInput
          subject={condition.subject}
          kind={kind}
          pattern={condition.op === 'matches'}
          value={condition.value}
          catalog={catalog}
          onChange={(value) => onChange({ ...condition, value })}
        />
      )}
    </RowShell>
  )
}

/** The input for one value of a subject: typed by kind, an id picked from a list. */
function ValueInput({
  subject,
  kind,
  pattern = false,
  value,
  catalog,
  onChange,
}: {
  subject: string
  kind: SubjectKind
  pattern?: boolean
  value: RuleCondition['value']
  catalog: Catalog
  onChange: (value: string) => void
}) {
  const id = useId()
  const current = value === undefined ? '' : String(value)
  const common = { 'aria-label': t('rulesPage.value'), value: current, onChange: (e: { target: { value: string } }) => onChange(e.target.value) }
  const choices: { id: string; name: string }[] | null =
    kind === 'tag'
      ? catalog.tags
      : kind === 'option'
        ? (catalog.options[subject] ?? [])
        : kind === 'choice'
          ? (catalog.fields.find((f) => f.id === subject)?.choices ?? [])
          : null
  if (catalog.named && (kind === 'tag' || kind === 'option')) {
    const names = (kind === 'tag' ? catalog.tags : (catalog.options[subject] ?? [])).map((c) => c.name)
    return (
      <Combobox
        id={`${id}-value`}
        ariaLabel={t('rulesPage.value')}
        className={valueClassName}
        value={current}
        placeholder={t('rulesPage.pickOrName')}
        options={[...new Set([...names, ...(current ? [current] : [])])].map((name) => ({ value: name, label: name }))}
        onChange={onChange}
        onCreate={(name) => onChange(name.trim())}
      />
    )
  }
  if (choices) {
    return (
      <Combobox
        id={`${id}-value`}
        ariaLabel={t('rulesPage.value')}
        className={valueClassName}
        value={current}
        placeholder={choices.length === 0 ? t('rulesPage.noValues') : t('rulesPage.pick')}
        options={[
          ...(current && !choices.some((c) => c.id === current) ? [{ value: current, label: t('rules.missing') }] : []),
          ...choices.map((c) => ({ value: c.id, label: c.name })),
        ]}
        onChange={onChange}
      />
    )
  }
  if (kind === 'date') return <input type="date" className={`${inputClassName} ${valueClassName}`} required {...common} />
  if (kind === 'number') {
    // Confidence is a share; every other number is the document's own, negative included.
    const share = subject === SUBJECT_CONFIDENCE ? { min: 0, max: 1, step: 0.05 } : { step: 'any' }
    return <input type="number" {...share} className={`${inputClassName} ${valueClassName}`} required {...common} />
  }
  if (kind === 'ai') {
    // A sentence, so a line of its own that wraps rather than a field that scrolls.
    return (
      <textarea
        rows={2}
        className={`${inputClassName} basis-full resize-y`}
        required
        maxLength={500}
        placeholder={t('rulesPage.aiQuestionPlaceholder')}
        {...common}
      />
    )
  }
  return (
    <input
      className={`${inputClassName} ${valueClassName} ${pattern ? 'font-mono' : ''}`}
      required
      maxLength={500}
      placeholder={pattern ? t('rulesPage.patternPlaceholder') : ''}
      {...common}
    />
  )
}

function ActionRow({
  index,
  action,
  catalog,
  onChange,
  onRemove,
}: {
  index: number
  action: RuleAction
  catalog: Catalog
  onChange: (a: RuleAction) => void
  onRemove: () => void
}) {
  const id = useId()
  const label = t('rulesPage.actionN', { n: index + 1 })
  const setField = action.field ?? ''
  const setKind = subjectKind(setField, catalog.fields)
  return (
    <RowShell label={label} onRemove={onRemove}>
      <Combobox
        id={`${id}-type`}
        ariaLabel={t('rulesPage.actionType')}
        className={pickerClassName}
        value={action.type}
        options={ACTION_TYPES.map((type) => ({ value: type, label: ACTION_LABELS[type] }))}
        onChange={(type) => onChange(defaultAction(type as RuleActionType))}
      />

      {(action.type === 'add_tag' || action.type === 'remove_tag') && (
        <ValueInput
          subject=""
          kind="tag"
          value={action.value}
          catalog={catalog}
          onChange={(value) => onChange({ ...action, value })}
        />
      )}

      {action.type === 'set_field' && (
        <>
          <Combobox
            id={`${id}-field`}
            ariaLabel={t('rulesPage.field')}
            className={pickerClassName}
            value={setField}
            options={subjectOptions(catalog, false, setField)}
            onChange={(field) => onChange({ ...action, field, value: '' })}
          />
          {setKind && (
            <ValueInput
              subject={setField}
              kind={setKind}
              value={action.value}
              catalog={catalog}
              onChange={(value) => onChange({ ...action, value })}
            />
          )}
        </>
      )}

      {action.type === 'require_review' && <p className={`${fieldHintClassName} py-2`}>{t('rulesPage.reviewHint')}</p>}

      {action.type === 'ai_extract' && (
        <div className="flex w-full flex-col gap-2">
          {catalog.fields.length === 0 ? (
            <p className={fieldHintClassName}>{t('rulesPage.aiNoFields')}</p>
          ) : (
            <fieldset className="flex flex-wrap gap-x-4 gap-y-1">
              <legend className={`${labelTextClassName} mb-1`}>{t('rulesPage.aiFields')}</legend>
              {catalog.fields.map((field) => (
                <label key={field.id} className="flex items-center gap-1.5 text-sm text-ink">
                  <input
                    type="checkbox"
                    className="accent-oxblood"
                    checked={(action.fields ?? []).includes(field.id)}
                    onChange={(e) =>
                      onChange({
                        ...action,
                        fields: e.target.checked
                          ? [...(action.fields ?? []), field.id]
                          : (action.fields ?? []).filter((id) => id !== field.id),
                      })
                    }
                  />
                  {field.name}
                </label>
              ))}
            </fieldset>
          )}
          <label className={labelClassName}>
            <span className={labelTextClassName}>{t('rulesPage.aiPrompt')}</span>
            <textarea
              rows={3}
              className={inputClassName}
              maxLength={4000}
              placeholder={t('rulesPage.aiPromptPlaceholder')}
              value={action.prompt ?? ''}
              onChange={(e) => onChange({ ...action, prompt: e.target.value })}
            />
          </label>
          <p className={fieldHintClassName}>{t('rulesPage.aiHint')}</p>
        </div>
      )}

      {action.type === 'webhook' && (
        <div className="flex w-full flex-col gap-2">
          <label className={labelClassName}>
            <span className={labelTextClassName}>{t('rulesPage.webhookUrl')}</span>
            <input
              type="url"
              className={inputClassName}
              required
              maxLength={500}
              placeholder="https://n8n.example.com/webhook/invoices"
              value={action.url ?? ''}
              onChange={(e) => onChange({ ...action, url: e.target.value })}
            />
          </label>
          <label className={labelClassName}>
            <span className={labelTextClassName}>{t('rulesPage.webhookSecret')}</span>
            <input
              className={inputClassName}
              maxLength={500}
              autoComplete="off"
              value={action.secret ?? ''}
              onChange={(e) => onChange({ ...action, secret: e.target.value })}
            />
          </label>
          <p className={fieldHintClassName}>{t('rulesPage.webhookHint')}</p>
        </div>
      )}
    </RowShell>
  )
}
