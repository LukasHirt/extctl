import { ref, readonly } from 'vue'
import { useLLM, type LLMConfig, type ChatMessage } from './useLLM'

// Shared module-level state for cross-component panel communication
const _panelOpen = ref(false)
const _pendingResources = ref<SynthesisResource[]>([])
const _llmConfig = ref<LLMConfig | null>(null)

// Minimum file count for the synthesize action to be visible
export const MIN_FILES = 2
// Maximum file count (spec: 2-10)
export const MAX_FILES = 10
// When estimated tokens exceed this fraction of context window, use multi-pass
const MULTIPASS_RATIO = 0.6
// Conservative context budget if probe has not yet settled
const FALLBACK_CONTEXT_TOKENS = 8192

export interface SynthesisResource {
  id: string
  name: string
  webDavPath: string
}

export function useSynthesizer() {
  function openPanel(resources: SynthesisResource[], config: LLMConfig): void {
    _pendingResources.value = resources.slice(0, MAX_FILES)
    _llmConfig.value = config
    _panelOpen.value = true
  }

  function closePanel(): void {
    _panelOpen.value = false
  }

  async function fetchContent(resource: SynthesisResource): Promise<string> {
    const r = await fetch(resource.webDavPath, {
      credentials: 'include',
      headers: { Accept: 'text/plain, text/markdown, text/*, */*' }
    })
    if (!r.ok) {
      throw new Error(`Failed to fetch "${resource.name}": ${r.status} ${r.statusText}`)
    }
    return r.text()
  }

  async function synthesize(
    resources: SynthesisResource[],
    config: LLMConfig,
    onChunk: (text: string) => void
  ): Promise<void> {
    const llm = useLLM(config)

    // Fetch all file contents in parallel while the LLM probe runs in background
    const contents = await Promise.all(resources.map(fetchContent))

    // Estimate token cost; by now probe has likely settled
    const estimatedTokens = contents.reduce((n, c) => n + Math.ceil(c.length / 4), 0)
    const contextTokens = llm.capabilities.value?.contextTokens ?? FALLBACK_CONTEXT_TOKENS
    const useMultiPass = estimatedTokens > contextTokens * MULTIPASS_RATIO

    const SYSTEM_PROMPT = `You are an assistant that synthesizes multiple documents into a structured overview.
Produce exactly three sections:
**Shared Themes** — common topics, patterns, or decisions across all documents
**Key Differences** — notable distinctions in content, tone, or scope
**Action Items** — concrete next steps extracted or inferred from the documents
Use bullet points within each section. Be concise and specific.`

    if (useMultiPass) {
      // Tier 2: small-context — summarize each file individually, then merge summaries
      const summaries = await Promise.all(
        resources.map((resource, i) =>
          llm.complete(
            [
              {
                role: 'system',
                content: 'Summarize the key points, decisions, and action items in this document in 3–5 bullet points.'
              },
              {
                role: 'user',
                content: `Document: "${resource.name}"\n\n${contents[i]}`
              }
            ],
            { maxTokens: 512 }
          )
        )
      )

      const mergeMessages: ChatMessage[] = [
        { role: 'system', content: SYSTEM_PROMPT },
        {
          role: 'user',
          content: `Synthesize the following ${resources.length} document summaries:\n\n${
            resources.map((r, i) => `### ${r.name}\n${summaries[i]}`).join('\n\n')
          }`
        }
      ]
      await llm.stream(mergeMessages, onChunk)
    } else {
      // Tier 1: large-context — all files in a single prompt pass
      const singlePassMessages: ChatMessage[] = [
        { role: 'system', content: SYSTEM_PROMPT },
        {
          role: 'user',
          content: `Synthesize these ${resources.length} documents:\n\n${
            resources.map((r, i) => `### ${r.name}\n\n${contents[i]}`).join('\n\n---\n\n')
          }`
        }
      ]
      await llm.stream(singlePassMessages, onChunk)
    }
  }

  async function saveAsMarkdown(
    content: string,
    filename: string,
    folderWebDavPath: string
  ): Promise<void> {
    const targetUrl = `${folderWebDavPath}/${filename}`
    const r = await fetch(targetUrl, {
      method: 'PUT',
      credentials: 'include',
      headers: { 'Content-Type': 'text/markdown; charset=utf-8' },
      body: content
    })
    if (!r.ok) {
      throw new Error(`Failed to save "${filename}": ${r.status} ${r.statusText}`)
    }
  }

  return {
    panelOpen: readonly(_panelOpen),
    pendingResources: readonly(_pendingResources),
    llmConfig: readonly(_llmConfig),
    openPanel,
    closePanel,
    fetchContent,
    synthesize,
    saveAsMarkdown
  }
}
