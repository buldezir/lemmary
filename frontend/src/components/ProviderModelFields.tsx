import { useId, useState } from 'react'
import {
  listProviderModels,
  modelOptionLabel,
  providerOptionLabel,
  providerServesPurpose,
  showsOCRModelWarning,
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
  // Only offer providers this binding can actually use. The API refuses the
  // rest anyway; showing them meant the only way to learn that a local endpoint
  // cannot do extraction was to save and read the error. A provider already
  // bound is kept in the list whatever its SDK, so an existing binding never
  // renders as blank.
  const eligible = providers.filter(
    (item) => item.id === providerId || providerServesPurpose(item.sdk, purpose),
  )
  const hideModel = purpose === 'ocr' && selected?.sdk === 'google_vision'
  const showWarning = purpose === 'ocr' && showsOCRModelWarning(selected?.sdk)

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
            onModelChange('')
          }}
        />
      </div>
      {!hideModel && (
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
