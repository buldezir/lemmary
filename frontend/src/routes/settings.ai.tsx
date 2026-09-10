import { type SubmitEvent, useState } from 'react'

import { listAIProviders } from '../lib/api/providers'
import { getEmbeddingStats, type EmbeddingStats } from '../lib/api/settings'
import { ProviderModelFields } from '../components/ProviderModelFields'
import { ProvidersBlock } from '../components/settings/ProvidersBlock'
import {
  ManagedByHostNotice,
  ResultDialog,
  SaveSettingsButton,
  SettingsLoading,
} from '../components/settings/SettingsFeedback'
import { useAppMeta } from '../hooks/useAppMeta'
import { useAsync } from '../hooks/useAsync'
import { useSettingsForm } from '../hooks/useSettingsForm'
import {
  Button,
  fieldHintClassName,
  sectionClassName,
  sectionTitleClassName,
} from '../components/ui'

type Bindings = {
  ocr_provider_id: string
  ocr_model: string
  extract_provider_id: string
  extract_model: string
  chat_provider_id: string
  chat_model: string
  search_provider_id: string
  search_model: string
  search_helper_provider_id: string
  search_helper_model: string
  embedding_provider_id: string
  embedding_model: string
}

// The four LLM bindings the simple view collapses into one "general" model.
// An empty Deep Search helper still fits: it means the search model does that
// work itself, which is the same model either way.
function fitsOneGeneralModel(form: Bindings): boolean {
  const general = `${form.extract_provider_id}|${form.extract_model}`
  const helper = `${form.search_helper_provider_id}|${form.search_helper_model}`
  return (
    `${form.chat_provider_id}|${form.chat_model}` === general &&
    `${form.search_provider_id}|${form.search_model}` === general &&
    (helper === general || helper === '|')
  )
}

/** The simple view has one LLM binding; the other three follow it. */
function setGeneralModel(providerId: string, model: string): Partial<Bindings> {
  return {
    extract_provider_id: providerId,
    extract_model: model,
    chat_provider_id: providerId,
    chat_model: model,
    search_provider_id: providerId,
    search_model: model,
    // Named rather than left empty: empty means the same thing here, but a
    // model spelled out is what Advanced setup then shows.
    search_helper_provider_id: providerId,
    search_helper_model: model,
  }
}

// EmbeddingStatsLine says how much of the archive the chosen model has actually
// covered. Without it, switching the model looks instantaneous while the
// backfill is in fact working through the archive a batch a minute, and nothing
// on the page would say so.
function EmbeddingStatsLine({ stats }: { stats: EmbeddingStats | null }) {
  if (!stats || !stats.enabled) return null

  const parts = [`${stats.embedded} of ${stats.total} documents embedded`]
  if (stats.dims > 0) parts.push(`${stats.dims} dimensions`)
  if (stats.chunks > 0) parts.push(`${stats.chunks} passages`)
  if (stats.pending > 0) parts.push(`${stats.pending} queued`)
  if (stats.failed > 0) parts.push(`${stats.failed} failed`)

  return <p className={fieldHintClassName}>{parts.join(' · ')}.</p>
}

export function SettingsAIPage() {
  // unknown/failed meta counts as managed; see AppMeta.aiManaged
  const { aiManaged, chatgptLogin } = useAppMeta()
  const [advancedModels, setAdvancedModels] = useState(false)
  const { data: providers, reload: reloadProviders } = useAsync(listAIProviders, [])
  // Allowed to fail, and loaded apart from the settings: it scans two tables,
  // and a slow or broken count must not keep the form off the screen.
  const { data: embeddingStats } = useAsync(
    () => getEmbeddingStats().catch(() => null),
    [],
  )
  const {
    form,
    loading,
    error,
    success,
    saving,
    updateField,
    updateFields,
    save,
    setError,
    setSuccess,
    closeResult,
  } = useSettingsForm<Bindings>((settings) => ({
    ocr_provider_id: settings.ocr_provider_id,
    ocr_model: settings.ocr_model,
    extract_provider_id: settings.extract_provider_id,
    extract_model: settings.extract_model,
    chat_provider_id: settings.chat_provider_id,
    chat_model: settings.chat_model,
    search_provider_id: settings.search_provider_id,
    search_model: settings.search_model,
    search_helper_provider_id: settings.search_helper_provider_id,
    search_helper_model: settings.search_helper_model,
    embedding_provider_id: settings.embedding_provider_id,
    embedding_model: settings.embedding_model,
  }))

  async function onSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!form) return
    // No client-side validation: which provider may serve which binding, and
    // which of them needs a model, is the server's answer -- see
    // applySettingsPatch.
    await save(form)
  }

  if (aiManaged !== false) return <ManagedByHostNotice />
  if (loading || !form) return <SettingsLoading error={error} />

  return (
    <>
      <ProvidersBlock
        providers={providers ?? []}
        chatgptLogin={chatgptLogin}
        onChanged={reloadProviders}
        onError={setError}
        onSuccess={setSuccess}
      />

      <form onSubmit={onSubmit}>
        <section className={sectionClassName}>
          <h2 className={sectionTitleClassName}>Models</h2>
          <div className="mb-4 flex items-center justify-between gap-3">
            <p className={fieldHintClassName}>
              {advancedModels
                ? 'Every job has its own model.'
                : fitsOneGeneralModel(form)
                  ? 'One model does extraction, chat and search.'
                  : 'Extraction, chat and search are on different models: open Advanced setup to see them. Picking a model here puts all three on it.'}
            </p>
            <Button
              variant="secondary"
              size="xs"
              onClick={() => {
                // Collapsing on the way in, not on save: the simple view
                // shows one model, so the three it hides must already agree.
                if (advancedModels) {
                  updateFields((current) =>
                    setGeneralModel(current.extract_provider_id, current.extract_model),
                  )
                }
                setAdvancedModels(!advancedModels)
              }}
            >
              {advancedModels ? 'Simple setup' : 'Advanced setup'}
            </Button>
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <ProviderModelFields
              label="OCR"
              help="Reads the text out of uploaded PDFs, images and scans. Plain text, CSV, Word and Excel files are read locally and skip this step."
              providers={providers ?? []}
              providerId={form.ocr_provider_id}
              model={form.ocr_model}
              purpose="ocr"
              onProviderChange={(id) => updateField('ocr_provider_id', id)}
              onModelChange={(value) => updateField('ocr_model', value)}
            />
            {!advancedModels && (
              <ProviderModelFields
                label="General AI"
                help="Reads documents into metadata, answers questions on Ask AI, and runs Deep Search. Advanced setup splits this into one model per job."
                providers={providers ?? []}
                providerId={form.extract_provider_id}
                model={form.extract_model}
                purpose="llm"
                onProviderChange={(id) =>
                  updateFields((current) => setGeneralModel(id, current.extract_model))
                }
                onModelChange={(value) =>
                  updateFields((current) => setGeneralModel(current.extract_provider_id, value))
                }
              />
            )}

            {advancedModels && (
              <>
                <ProviderModelFields
                  label="Extraction"
                  help="Turns a document's text into its title, date, type, correspondent, tags and summary. Also proposes the cuts for Detect automatically when splitting a PDF."
                  providers={providers ?? []}
                  providerId={form.extract_provider_id}
                  model={form.extract_model}
                  purpose="llm"
                  onProviderChange={(id) => updateField('extract_provider_id', id)}
                  onModelChange={(value) => updateField('extract_model', value)}
                />
                <ProviderModelFields
                  label="Chat"
                  help="Answers questions about a single document on its Ask AI page. Leave the provider empty to turn the feature off."
                  providers={providers ?? []}
                  providerId={form.chat_provider_id}
                  model={form.chat_model}
                  purpose="llm"
                  allowEmpty
                  onProviderChange={(id) => updateField('chat_provider_id', id)}
                  onModelChange={(value) => updateField('chat_model', value)}
                />
                <ProviderModelFields
                  label="Search"
                  help="Answers natural-language queries on the Deep Search page, in both Search and Research mode. Leave the provider empty to turn the feature off."
                  providers={providers ?? []}
                  providerId={form.search_provider_id}
                  model={form.search_model}
                  purpose="llm"
                  allowEmpty
                  onProviderChange={(id) => updateField('search_provider_id', id)}
                  onModelChange={(value) => updateField('search_model', value)}
                />
                <ProviderModelFields
                  label="Deep Search helper"
                  help="Cheaper model Deep Search uses to read and extract from many documents at once: it turns long reads into notes and surveys whole topics one document at a time. Leave empty to have the Search model do this work itself."
                  providers={providers ?? []}
                  providerId={form.search_helper_provider_id}
                  model={form.search_helper_model}
                  purpose="llm"
                  allowEmpty
                  onProviderChange={(id) => updateField('search_helper_provider_id', id)}
                  onModelChange={(value) => updateField('search_helper_model', value)}
                />
              </>
            )}

            <ProviderModelFields
              label="Embeddings"
              help="Lets Deep Search find documents by meaning as well as by keyword, which is what makes a question phrased in one language reach a document written in another. Leave the provider empty to search by keyword only."
              providers={providers ?? []}
              providerId={form.embedding_provider_id}
              model={form.embedding_model}
              purpose="embedding"
              allowEmpty
              onProviderChange={(id) => updateField('embedding_provider_id', id)}
              onModelChange={(value) => updateField('embedding_model', value)}
            />
            <div className="sm:col-span-2">
              <EmbeddingStatsLine stats={embeddingStats} />
            </div>
          </div>
          <div className="mt-4">
            <SaveSettingsButton saving={saving} />
          </div>
        </section>
      </form>

      <ResultDialog error={error} success={success} onClose={closeResult} />
    </>
  )
}
