import { useEffect, useState } from 'react'
import {
  listProviderModels,
  type AIProvider,
  type CatalogModel,
  type ProviderSDK,
} from '../lib/pocketbase'

const inputClassName =
  'w-full rounded-md border border-stone-300 bg-stone-50 px-3 py-2 text-sm outline-none focus:border-gray-900 focus:ring-1 focus:ring-gray-900'
const labelClassName = 'flex flex-col gap-1'
const labelTextClassName = 'text-xs font-medium text-stone-500'

const CUSTOM_MODEL = '__custom__'

export const OCR_MODEL_WARNING =
  'Choose this model wisely — this provider does not advertise which models accept file inputs.'

export function showsOCRModelWarning(sdk?: string) {
  return sdk === 'openai'
}

export function isLLMProvider(sdk: string) {
  return sdk === 'openai' || sdk === 'openrouter'
}

export function sdkLabel(sdk: ProviderSDK | string) {
  switch (sdk) {
    case 'openai':
      return 'OpenAI'
    case 'openrouter':
      return 'OpenRouter'
    case 'google_vision':
      return 'Google Cloud Vision'
    case 'mistral':
      return 'Mistral OCR'
    default:
      return sdk
  }
}

export function providerOptionLabel(item: Pick<AIProvider, 'alias' | 'sdk'>) {
  const sdk = sdkLabel(item.sdk)
  return item.alias === sdk ? item.alias : `${item.alias} (${sdk})`
}

export function modelOptionLabel(item: CatalogModel) {
  if (item.name && item.name !== item.id) {
    return `${item.id} (${item.name})`
  }
  return item.id
}

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
  providers,
  providerId,
  model,
  purpose,
  onProviderChange,
  onModelChange,
  allowEmpty = false,
}: ProviderModelFieldsProps) {
  const [models, setModels] = useState<CatalogModel[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  const selected = providers.find((item) => item.id === providerId)
  const hideModel = purpose === 'ocr' && selected?.sdk === 'google_vision'
  const showWarning = purpose === 'ocr' && showsOCRModelWarning(selected?.sdk)

  useEffect(() => {
    if (!providerId || hideModel) {
      setModels([])
      setError('')
      setLoading(false)
      return
    }

    let active = true
    async function load() {
      try {
        setLoading(true)
        setError('')
        const next = await listProviderModels(providerId, purpose)
        if (!active) return
        setModels(next.models)
      } catch (err) {
        if (!active) return
        setModels([])
        setError(err instanceof Error ? err.message : 'Failed to load models')
      } finally {
        if (active) setLoading(false)
      }
    }
    void load()
    return () => {
      active = false
    }
  }, [providerId, purpose, hideModel])

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
          loading={loading}
          allowEmpty={allowEmpty}
          onChange={onModelChange}
        />
      )}
      {error && <p className="text-xs text-amber-700 sm:col-span-2">{error}. You can still type a model id.</p>}
      {showWarning && (
        <p className="text-xs text-amber-800 sm:col-span-2">{OCR_MODEL_WARNING}</p>
      )}
    </div>
  )
}
