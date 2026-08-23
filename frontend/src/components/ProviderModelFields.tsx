import { useState } from 'react'
import {
  listProviderModels,
  modelOptionLabel,
  providerOptionLabel,
  showsOCRModelWarning,
  OCR_MODEL_WARNING,
  type AIProvider,
  type CatalogModel,
} from '../lib/api/providers'
import { useAsync } from '../hooks/useAsync'
import { fieldHintClassName, inputClassName, labelClassName, labelTextClassName } from './ui'

const CUSTOM_MODEL = '__custom__'

type ModelSelectProps = {
  label: string
  model: string
  models: CatalogModel[]
  loading: boolean
  onChange: (model: string) => void
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
  const inCatalog = models.some((item) => item.id === model)
  const [wantCustom, setWantCustom] = useState(false)
  const showCustomInput =
    !loading && models.length > 0 && (wantCustom || Boolean(model && !inCatalog))
  const useSelect = loading || models.length > 0
  const selectValue = showCustomInput ? CUSTOM_MODEL : model

  return (
    <div className={labelClassName}>
      <label className={labelClassName}>
        <span className={labelTextClassName}>{label} model</span>
        {useSelect ? (
          <select
            className={`${inputClassName} disabled:cursor-not-allowed disabled:opacity-50`}
            value={loading ? '' : selectValue}
            disabled={disabled || loading}
            onChange={(event) => {
              const next = event.target.value
              if (next === CUSTOM_MODEL) {
                setWantCustom(true)
                if (inCatalog) onChange('')
                return
              }
              setWantCustom(false)
              onChange(next)
            }}
          >
            {loading ? (
              <option value="">Loading models…</option>
            ) : (
              <>
                {allowEmpty || !selectValue ? (
                  <option value="">{allowEmpty ? 'None' : 'Select a model'}</option>
                ) : null}
                {models.map((item) => (
                  <option key={item.id} value={item.id}>
                    {modelOptionLabel(item)}
                  </option>
                ))}
                <option value={CUSTOM_MODEL}>Custom model id…</option>
              </>
            )}
          </select>
        ) : (
          <input
            className={inputClassName}
            value={model}
            placeholder="Model id"
            disabled={disabled}
            onChange={(event) => onChange(event.target.value)}
          />
        )}
      </label>
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
  purpose: 'ocr' | 'llm'
  onProviderChange: (providerId: string) => void
  onModelChange: (model: string) => void
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
  const selected = providers.find((item) => item.id === providerId)
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
      <label className={labelClassName}>
        <span className={labelTextClassName}>{label} provider</span>
        <select
          className={inputClassName}
          value={providerId}
          onChange={(event) => {
            onProviderChange(event.target.value)
            onModelChange('')
          }}
        >
          {allowEmpty || providers.length === 0 || !providerId ? (
            <option value="">Select a provider</option>
          ) : null}
          {providers.map((item) => (
            <option key={item.id} value={item.id}>
              {providerOptionLabel(item)}
            </option>
          ))}
        </select>
      </label>
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
