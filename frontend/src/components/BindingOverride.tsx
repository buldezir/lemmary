import { useEffect, useRef } from 'react'
import {
  asPickerProvider,
  bindingModelLabel,
  listPickableProviders,
  EMPTY_BINDING,
  type ModelPurpose,
  type ProviderBinding,
} from '../lib/api/providers'
import { STEP_BINDINGS, type JobOverrides } from '../lib/api/documents'
import type { ProcessingStep } from '../lib/processing'
import { useAsync } from '../hooks/useAsync'
import { ProviderModelFields } from './ProviderModelFields'
import { fieldHintClassName } from './ui'

type BindingOverrideProps = {
  /** Names the binding being overridden, e.g. "Chat" or "Extraction". */
  label: string
  purpose: ModelPurpose
  /**
   * The chosen binding, or undefined for "use the configured model". Undefined
   * rather than an empty binding, because an empty one is the real state of an
   * open picker with no provider chosen yet.
   */
  value: ProviderBinding | undefined
  onChange: (binding: ProviderBinding | undefined) => void
  /** Short explanation of what this binding is used for. */
  help?: string
  /**
   * Locks the choice on its current value, for a conversation that already has
   * turns: it must show what it runs on without offering to change it.
   */
  locked?: boolean
  /** What the locked state says in place of the picker. */
  lockedHint?: string
  /**
   * Names the model that answers when nothing is overridden. Off for the
   * reprocess forms, where it would cost a request per binding on every page
   * load to say what the step history already records.
   */
  showConfigured?: boolean
  /**
   * Names which configured binding to report as the default, when the purpose
   * alone cannot say -- "search" and "chat" are both `llm`.
   */
  bindingName?: string
}

/**
 * An optional provider/model choice for one chat or one reprocess job. Off means
 * the request carries no binding at all, which is why this wraps
 * ProviderModelFields rather than using it directly: in Settings a binding
 * always exists, and here it usually should not.
 */
export function BindingOverride({
  label,
  purpose,
  value,
  onChange,
  help,
  locked = false,
  lockedHint,
  showConfigured = false,
  bindingName,
}: BindingOverrideProps) {
  const open = value !== undefined

  // The binding as it stands *within* the current event, not as of the last
  // render: ProviderModelFields reports a provider change as onProviderChange
  // then onModelChange(''), both before React re-renders, so merging into the
  // `value` prop computes the second update from the pre-change binding and
  // leaves the picker stuck on "Select a provider".
  // Synced in an effect rather than during render, which React forbids; the
  // handlers below keep it current within an event themselves.
  const latest = useRef(value)
  useEffect(() => {
    latest.current = value
  }, [value])

  function update(next: ProviderBinding | undefined) {
    latest.current = next
    onChange(next)
  }

  const providersState = useAsync(async () => {
    // Fetched only when the picker is open or the configured model is named;
    // both answers come from the same request, and a page showing neither pays
    // for nothing.
    if (!open && !showConfigured) return { providers: [], configured: {} }
    return listPickableProviders(purpose, bindingName)
  }, [open, showConfigured, purpose, bindingName])
  const providers = (providersState.data?.providers ?? []).map(asPickerProvider)
  const configured = providersState.data?.configured ?? {}

  const effectiveModel = value?.provider_id ? value.model : configured.model
  const source = value?.provider_id ? '' : ' (from Settings)'

  if (locked) {
    if (!value?.provider_id && !showConfigured) return null
    return (
      <div className="flex flex-col gap-1">
        <p className="text-xs text-ink-soft">
          {label} model:{' '}
          <span className="font-medium text-ink">{bindingModelLabel(effectiveModel)}</span>
          {source}
        </p>
        {value?.provider_id && lockedHint && <p className={fieldHintClassName}>{lockedHint}</p>}
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-3">
      {/* Which model answers, whether or not it was chosen here: the only other
          way to know is Settings, which most accounts cannot open. */}
      {showConfigured && !open && (
        <p className="text-xs text-ink-soft">
          {label} model:{' '}
          <span className="font-medium text-ink">{bindingModelLabel(configured.model)}</span>
          {configured.provider_name ? ` (${configured.provider_name}, from Settings)` : ' (from Settings)'}
        </p>
      )}
      <label className="flex items-center gap-2 text-sm text-ink-muted">
        <input
          type="checkbox"
          checked={open}
          onChange={(event) => update(event.target.checked ? { ...EMPTY_BINDING } : undefined)}
          className="size-4 rounded-xs border-line-strong text-oxblood focus:ring-oxblood"
        />
        Use a different {label.toLowerCase()} model
      </label>

      {open && (
        <>
          <ProviderModelFields
            label={label}
            help={help}
            providers={providers}
            providerId={value.provider_id}
            model={value.model}
            purpose={purpose}
            onProviderChange={(provider_id) => update({ provider_id, model: '' })}
            onModelChange={(model) => update({ ...(latest.current ?? EMPTY_BINDING), model })}
          />
          {providersState.error && <p className="text-xs text-amber-700">{providersState.error}</p>}
          {!providersState.loading && providers.length === 0 && !providersState.error && (
            <p className="text-xs text-amber-800">
              No configured provider can serve this binding. Add one in Settings.
            </p>
          )}
        </>
      )}
    </div>
  )
}

/**
 * How each overridable step is presented; STEP_BINDINGS says which binding it
 * reads. Both shapes of the picker read this one table, so there is one
 * embedding warning. Not exported: a constant exported from a file that also
 * exports components costs Fast Refresh.
 */
const JOB_BINDINGS = [
  {
    step: 'ocr',
    purpose: 'ocr',
    label: 'OCR',
    help: 'Reads the text out of the document.',
  },
  {
    step: 'extract_metadata',
    purpose: 'llm',
    label: 'Extraction',
    help: "Turns the document's text into its title, date, type and tags.",
  },
  {
    step: 'embed',
    purpose: 'embedding',
    label: 'Embedding',
    help: 'Builds the retrieval vectors. Must name the model already bound in Settings — vectors from any other model are written and never read, because the search index only reads the configured one.',
  },
] as const satisfies readonly {
  // Narrower than ProcessingStep on purpose: a step with no binding cannot be
  // listed here.
  step: keyof typeof STEP_BINDINGS
  purpose: ModelPurpose
  label: string
  help: string
}[]

/**
 * The model override for one pipeline step, or nothing for a step that calls no
 * provider.
 */
export function StepBindingOverride({
  step,
  value,
  onChange,
}: {
  step: ProcessingStep
  value: JobOverrides
  onChange: (overrides: JobOverrides) => void
}) {
  const binding = JOB_BINDINGS.find((item) => item.step === step)
  if (!binding) return null
  const key = STEP_BINDINGS[binding.step]
  return (
    <BindingOverride
      label={binding.label}
      purpose={binding.purpose}
      value={value[key]}
      onChange={(next) => onChange({ ...value, [key]: next })}
      help={binding.help}
    />
  )
}

/**
 * All three bindings in one block, for the pages that queue a reprocess by mode
 * rather than by ticking steps. All three show whatever the mode is, because
 * "Auto" does not know which steps it will run until the server looks at each
 * document.
 */
export function JobOverrideFields({
  value,
  onChange,
}: {
  value: JobOverrides
  onChange: (overrides: JobOverrides) => void
}) {
  return (
    <div className="flex flex-col gap-3 border-t border-line pt-3">
      {JOB_BINDINGS.map((binding) => (
        <BindingOverride
          key={binding.step}
          label={binding.label}
          purpose={binding.purpose}
          value={value[STEP_BINDINGS[binding.step]]}
          onChange={(next) => onChange({ ...value, [STEP_BINDINGS[binding.step]]: next })}
          help={binding.help}
        />
      ))}
    </div>
  )
}
