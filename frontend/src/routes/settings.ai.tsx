import { type SubmitEvent } from 'react'

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
import { fieldHintClassName, sectionClassName, sectionTitleClassName } from '../components/ui'

type Bindings = {
  ocr_provider_id: string
  ocr_model: string
  extract_provider_id: string
  extract_model: string
  research_provider_id: string
  research_model: string
  embedding_provider_id: string
  embedding_model: string
  // No model beside it: a web-search API takes none.
  websearch_provider_id: string
}

// Without this, switching the embedding model looks instantaneous while the
// backfill is in fact working through the archive a batch a minute.
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
  const { aiManaged, metaLoaded } = useAppMeta()
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
    save,
    setError,
    setSuccess,
    closeResult,
  } = useSettingsForm<Bindings>((settings) => ({
    ocr_provider_id: settings.ocr_provider_id,
    ocr_model: settings.ocr_model,
    extract_provider_id: settings.extract_provider_id,
    extract_model: settings.extract_model,
    research_provider_id: settings.research_provider_id,
    research_model: settings.research_model,
    embedding_provider_id: settings.embedding_provider_id,
    embedding_model: settings.embedding_model,
    websearch_provider_id: settings.websearch_provider_id,
  }))

  async function onSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!form) return
    // No client-side validation: which provider may serve which binding is the
    // server's answer, see applySettingsPatch.
    await save(form)
  }

  // undefined means the meta request is still out, and claiming a hosting
  // provider owns these settings is a statement, not a safe default.
  if (!metaLoaded) return <SettingsLoading error="" />
  if (aiManaged !== false) return <ManagedByHostNotice />
  if (loading || !form) return <SettingsLoading error={error} />

  return (
    <>
      <ProvidersBlock
        providers={providers ?? []}
        onChanged={reloadProviders}
        onError={setError}
        onSuccess={setSuccess}
      />

      <form onSubmit={onSubmit}>
        <section className={sectionClassName}>
          <h2 className={sectionTitleClassName}>Models</h2>
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
            <ProviderModelFields
              label="General AI"
              help="Reads documents into metadata, answers questions on Ask AI, runs AI assisted search, and does Deep Research's document reads."
              providers={providers ?? []}
              providerId={form.extract_provider_id}
              model={form.extract_model}
              purpose="llm"
              onProviderChange={(id) => updateField('extract_provider_id', id)}
              onModelChange={(value) => updateField('extract_model', value)}
            />
            <ProviderModelFields
              label="Advanced"
              help="The Advanced model drives the Deep Research reasoning loop: a few expensive calls per question where everything else is many cheap ones. Leave empty to run it on General AI."
              providers={providers ?? []}
              providerId={form.research_provider_id}
              model={form.research_model}
              purpose="llm"
              allowEmpty
              onProviderChange={(id) => updateField('research_provider_id', id)}
              onModelChange={(value) => updateField('research_model', value)}
            />

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

            <ProviderModelFields
              label="Web search"
              help="Lets Deep Research and Ask AI look things up online when the archive cannot answer -- a rate that changed, a company's present details. Off unless a provider is bound here, and then still off in a chat until the reader turns it on. Every lookup is billed by the provider."
              providers={providers ?? []}
              providerId={form.websearch_provider_id}
              model=""
              purpose="websearch"
              allowEmpty
              onProviderChange={(id) => updateField('websearch_provider_id', id)}
              onModelChange={() => {}}
            />
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
