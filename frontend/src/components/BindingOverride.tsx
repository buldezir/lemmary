import { useEffect, useRef } from 'react'
import {
  asPickerProvider,
  bindingModelLabel,
  listPickableProviders,
  EMPTY_BINDING,
  type ModelPurpose,
  type ProviderBinding,
} from '../lib/api/providers'
import type { JobOverrides } from '../lib/api/documents'
import type { ProcessingStep } from '../lib/processing'
import { useAsync } from '../hooks/useAsync'
import { ProviderModelFields } from './ProviderModelFields'
import { fieldHintClassName } from './ui'

type BindingOverrideProps = {
  /** Names the binding being overridden, e.g. "Chat" or "Extraction". */
  label: string
  purpose: ModelPurpose
  /**
   * The chosen binding, or undefined for "use the configured model".
   *
   * Undefined is the off state rather than an empty binding, because an empty
   * one is a real intermediate state: the picker is open and no provider has
   * been chosen yet. Collapsing the two would make the checkbox unable to open.
   */
  value: ProviderBinding | undefined
  onChange: (binding: ProviderBinding | undefined) => void
  /** Short explanation of what this binding is used for. */
  help?: string
  /**
   * Locks the choice open on its current value. For a conversation that already
   * has turns: the binding is fixed once a transcript exists, so the picker has
   * to show what it is running on without offering to change it.
   */
  locked?: boolean
  /** What the locked state says in place of the picker. */
  lockedHint?: string
  /**
   * Names the model that answers when nothing is overridden.
   *
   * For the chat surfaces, where "which model am I talking to?" is a fair
   * question whether or not anyone touched the picker. Off for the reprocess
   * forms, where it would cost a request per binding on every page load to
   * answer something the step history already records after the fact.
   */
  showConfigured?: boolean
  /**
   * Names which configured binding to report as the default, when the purpose
   * alone cannot say -- "search" and "chat" are both `llm`. Passed through to
   * the providers endpoint.
   */
  bindingName?: string
}

/**
 * An optional provider/model choice for one chat or one reprocess job.
 *
 * Off by default, and off means the request carries no binding at all -- so a
 * page whose checkbox is never ticked behaves exactly as it did before
 * overrides existed. That default is the whole reason this wraps
 * ProviderModelFields rather than using it directly: in Settings a binding
 * always exists, and on these five surfaces it usually should not.
 *
 * The provider list comes from /api/app/ai/providers, which any signed-in user
 * may read; ProviderModelFields fetches the model catalogue per provider.
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
  // render.
  //
  // ProviderModelFields reports a provider change as two calls in one handler:
  // onProviderChange(next), then onModelChange('') to drop the model that
  // belonged to the old provider. Both run before React re-renders, so a
  // handler that merged into the `value` prop would compute the second update
  // from the pre-change binding -- and the model reset would put the empty
  // provider back, leaving the picker stuck on "Select a provider" however many
  // times it was clicked. Settings does not hit this: each of its two callbacks
  // writes a different field of one settings object.
  // Synced in an effect rather than assigned during render, which React
  // forbids; the handlers below keep it current within an event themselves,
  // which is the case that matters here.
  const latest = useRef(value)
  useEffect(() => {
    latest.current = value
  }, [value])

  function update(next: ProviderBinding | undefined) {
    latest.current = next
    onChange(next)
  }

  const providersState = useAsync(async () => {
    // Fetched when the picker is opened, and also when the caller wants the
    // configured model named -- that answer comes from the same request. A page
    // that shows neither pays for nothing: most visits to a reprocess form
    // override no model at all.
    if (!open && !showConfigured) return { providers: [], configured: {} }
    return listPickableProviders(purpose, bindingName)
  }, [open, showConfigured, purpose, bindingName])
  const providers = (providersState.data?.providers ?? []).map(asPickerProvider)
  const configured = providersState.data?.configured ?? {}

  // The model this binding actually runs on: the override when there is one,
  // the Settings binding when there is not.
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
      {/* Which model answers, whether or not it was chosen here. Without it the
          only way to know was to open Settings, which most accounts cannot. */}
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
 * The three pipeline steps that run on a model, and the job binding each one
 * uses. The other three steps -- preview, duplicate detection, applying the
 * metadata -- call no provider at all, so they have no model to override.
 *
 * One table, read by both shapes of the picker: grouped, for the pages that
 * queue a batch by mode, and per step, for the form that ticks steps
 * individually. Two copies would mean two versions of the embedding warning.
 *
 * Not exported: nothing outside needs it, and exporting a constant from a file
 * that also exports components costs Fast Refresh.
 */
const JOB_BINDINGS = [
  {
    step: 'ocr',
    key: 'ocr',
    purpose: 'ocr',
    label: 'OCR',
    help: 'Reads the text out of the document.',
  },
  {
    step: 'extract_metadata',
    key: 'extract',
    purpose: 'llm',
    label: 'Extraction',
    help: "Turns the document's text into its title, date, type and tags.",
  },
  {
    step: 'embed',
    key: 'embedding',
    purpose: 'embedding',
    label: 'Embedding',
    help: 'Builds the retrieval vectors. Must name the model already bound in Settings — vectors from any other model are written and never read, because the search index only reads the configured one.',
  },
] as const satisfies readonly {
  step: ProcessingStep
  key: keyof JobOverrides
  purpose: ModelPurpose
  label: string
  help: string
}[]

/**
 * The model override for one pipeline step, or nothing for a step that calls no
 * provider.
 *
 * Rendered beside the step it belongs to rather than in a block of its own: the
 * question "which model?" only means anything once you have said you are
 * re-running the step that uses one.
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
  return (
    <BindingOverride
      label={binding.label}
      purpose={binding.purpose}
      value={value[binding.key]}
      onChange={(next) => onChange({ ...value, [binding.key]: next })}
      help={binding.help}
    />
  )
}

/**
 * All three bindings in one block, for the pages that queue a reprocess by mode
 * rather than by ticking steps: the bulk bar on the document list and the
 * failed-processing panel.
 *
 * All three are offered whatever the mode is. "Auto" does not know which steps
 * it will run until the server looks at each document, and a picker that
 * appears and disappears as the mode dropdown moves is harder to reason about
 * than one a job simply ignores.
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
          key={binding.key}
          label={binding.label}
          purpose={binding.purpose}
          value={value[binding.key]}
          onChange={(next) => onChange({ ...value, [binding.key]: next })}
          help={binding.help}
        />
      ))}
    </div>
  )
}
