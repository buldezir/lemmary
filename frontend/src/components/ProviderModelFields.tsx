import { useId, useState } from 'react'
import {
  listProviderModels,
  modelOptionLabel,
  providerOptionLabel,
  recommendedModel,
  eligibleProviders,
  showsOCRModelWarning,
  localOCRModelHint,
  OCR_MODEL_WARNING,
  type AIProvider,
  type CatalogModel,
  type ModelPurpose,
} from '../lib/api/providers'
import { useAsync } from '../hooks/useAsync'
import { Combobox, type ComboboxOption } from './Combobox'
import { fieldHintClassName, inputClassName, labelClassName, labelTextClassName } from './ui'

const CUSTOM_MODEL = '__custom__'

type ModelSelectProps = {
  label: string
  model: string
  models: CatalogModel[]
  loading: boolean
  onChange: (model: string, meta?: CatalogModel) => void
  allowEmpty?: boolean
  disabled?: boolean
}

export function ModelSelect({
  label,
  model,
  models,
  loading,
  onChange,
  allowEmpty = false,
  disabled = false,
}: ModelSelectProps) {
  const inputId = useId()
  const inCatalog = models.some((item) => item.id === model)
  const [wantCustom, setWantCustom] = useState(false)
  const showCustomInput =
    !loading && models.length > 0 && (wantCustom || Boolean(model && !inCatalog))
  const useSelect = loading || models.length > 0
  const selectValue = showCustomInput ? CUSTOM_MODEL : model

  const options: ComboboxOption[] = [
    ...(allowEmpty ? [{ value: '', label: 'None', pinned: true }] : []),
    ...models.map((item) => ({ value: item.id, label: modelOptionLabel(item) })),
    { value: CUSTOM_MODEL, label: 'Custom model id…', pinned: true },
  ]

  return (
    <div className={labelClassName}>
      <label htmlFor={inputId} className={labelTextClassName}>
        {label} model
      </label>
      {useSelect ? (
        <Combobox
          id={inputId}
          value={selectValue}
          options={options}
          placeholder={allowEmpty ? 'None' : 'Select a model'}
          loading={loading}
          loadingLabel="Loading models…"
          disabled={disabled}
          onChange={(next) => {
            if (next === CUSTOM_MODEL) {
              setWantCustom(true)
              if (inCatalog) onChange('')
              return
            }
            setWantCustom(false)
            onChange(
              next,
              models.find((item) => item.id === next),
            )
          }}
        />
      ) : (
        <input
          id={inputId}
          className={inputClassName}
          value={model}
          placeholder="Model id"
          disabled={disabled}
          onChange={(event) => onChange(event.target.value)}
        />
      )}
      {showCustomInput ? (
        <input
          className={inputClassName}
          value={model}
          placeholder="Model id"
          aria-label={`Custom ${label} model`}
          disabled={disabled}
          onChange={(event) => onChange(event.target.value)}
        />
      ) : null}
    </div>
  )
}

type ProviderModelFieldsProps = {
  label: string
  /** Short explanation of what this provider/model pair is used for. */
  help?: string
  providers: AIProvider[]
  providerId: string
  model: string
  purpose: ModelPurpose
  onProviderChange: (providerId: string) => void
  onModelChange: (model: string, meta?: CatalogModel) => void
  allowEmpty?: boolean
}

export function ProviderModelFields({
  label,
  help,
  providers,
  providerId,
  model,
  purpose,
  onProviderChange,
  onModelChange,
  allowEmpty = false,
}: ProviderModelFieldsProps) {
  const providerInputId = useId()
  const selected = providers.find((item) => item.id === providerId)
  // Narrowed here, not by the caller, which must pass every provider it has:
  // narrowing earlier is what hid the local SDK from the embedding picker.
  const eligible = eligibleProviders(providers, purpose, providerId)
  // Web search never shows a model field: the SDKs behind it take none at all,
  // unlike OCR, where only some do.
  const hideModel =
    purpose === 'websearch' || (purpose === 'ocr' && selected?.sdk === 'google_vision')
  const showWarning = purpose === 'ocr' && showsOCRModelWarning(selected?.sdk)
  // A local sidecar has no catalogue, so the picker falls back to a free-text
  // box; bare, that box reads as a required model id.
  const localHint = purpose === 'ocr' ? localOCRModelHint(selected?.sdk) : ''

  const modelsState = useAsync(async () => {
    if (!providerId || hideModel) {
      return { models: [] as CatalogModel[], sdk: '' }
    }
    return listProviderModels(providerId, purpose)
  }, [providerId, purpose, hideModel])
  const models = modelsState.data?.models ?? []

  return (
    <div className="grid gap-4 sm:grid-cols-2 sm:col-span-2">
      <div className={labelClassName}>
        <label htmlFor={providerInputId} className={labelTextClassName}>
          {label} provider
        </label>
        <Combobox
          id={providerInputId}
          value={providerId}
          options={[
            ...(allowEmpty ? [{ value: '', label: 'None', pinned: true }] : []),
            ...eligible.map((item) => ({ value: item.id, label: providerOptionLabel(item) })),
          ]}
          placeholder={allowEmpty ? 'None' : 'Select a provider'}
          onChange={(next) => {
            onProviderChange(next)
            // The model the guide names for the new provider, not an empty box,
            // and empty for every SDK it has no answer for.
            onModelChange(recommendedModel(providers.find((item) => item.id === next)?.sdk, purpose))
          }}
        />
      </div>
      {/* No provider, no model field: ModelSelect reads an empty catalogue as
          "this provider has none" and falls back to a free-text box, which an
          unbound binding must not show. */}
      {!hideModel && providerId && (
        <ModelSelect
          key={providerId || 'none'}
          label={label}
          model={model}
          models={models}
          loading={modelsState.loading}
          allowEmpty={allowEmpty}
          onChange={onModelChange}
        />
      )}
      {localHint && <p className={`${fieldHintClassName} sm:col-span-2`}>{localHint}</p>}
      {help && <p className={`${fieldHintClassName} sm:col-span-2`}>{help}</p>}
      {modelsState.error && (
        <p className="text-xs text-amber-700 sm:col-span-2">
          {modelsState.error}. You can still type a model id.
        </p>
      )}
      {showWarning && <p className="text-xs text-amber-800 sm:col-span-2">{OCR_MODEL_WARNING}</p>}
    </div>
  )
}
