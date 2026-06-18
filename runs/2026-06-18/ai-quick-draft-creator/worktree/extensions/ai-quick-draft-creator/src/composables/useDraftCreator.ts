import { ref, type Ref } from 'vue'
import { useLLM } from './useLLM'
import type { LLMConfig, LLMCapabilities } from './useLLM'

export type DraftFormat = 'markdown' | 'text'

export interface DraftResult {
  content: string
  filename: string
  tier: 1 | 2
}

// Minimum context tokens to qualify for tier-1 (richly-sectioned) generation.
const TIER1_MIN_CONTEXT = 8192

export function selectTier(caps: LLMCapabilities): 1 | 2 {
  return caps.contextTokens >= TIER1_MIN_CONTEXT ? 1 : 2
}

export function filenameFromDescription(description: string, format: DraftFormat): string {
  const slug = description
    .toLowerCase()
    .replace(/[^a-z0-9\s-]/g, '')
    .trim()
    .replace(/\s+/g, '-')
    .slice(0, 50)
  const ext = format === 'markdown' ? '.md' : '.txt'
  return `${slug || 'draft'}${ext}`
}

export function buildPrompt(description: string, format: DraftFormat, tier: 1 | 2): string {
  if (tier === 1) {
    const formatNote =
      format === 'markdown'
        ? 'Format with Markdown: use ## for sections, ### for subsections, bullet lists, and tables where helpful.'
        : 'Use plain text with UPPERCASE SECTION LABELS and clear indentation.'
    return [
      'You are a professional document drafter.',
      `Create a richly structured document based on this description: "${description}"`,
      formatNote,
      'Requirements:',
      '  - Start with a title',
      '  - Include at least 3 named sections with placeholder content',
      '  - Add an "Action Items" or "Next Steps" section where relevant',
      '  - End with a "Summary" section',
      '  - Use [TODO: …] placeholders where the user must fill in specifics',
      'Respond with ONLY the document content — no preamble, no explanation.'
    ].join('\n')
  }

  const hint =
    format === 'markdown'
      ? 'Use simple Markdown (headings and bullet points are fine).'
      : 'Use plain text.'
  return `Draft a document for: "${description}". ${hint} Keep it clear and concise. Respond with ONLY the document content.`
}

export interface UseDraftCreatorReturn {
  generating: Ref<boolean>
  error: Ref<string | null>
  result: Ref<DraftResult | null>
  generate: (description: string, format: DraftFormat) => Promise<void>
}

export function useDraftCreator(llmConfig: LLMConfig): UseDraftCreatorReturn {
  const generating = ref(false)
  const error = ref<string | null>(null)
  const result = ref<DraftResult | null>(null)

  // Probe starts in the background immediately on useLLM creation.
  const llm = useLLM(llmConfig)

  async function generate(description: string, format: DraftFormat): Promise<void> {
    const trimmed = description.trim()
    if (!trimmed) {
      error.value = 'Please enter a description first.'
      return
    }

    generating.value = true
    error.value = null
    result.value = null

    try {
      // Capabilities are populated after the background probe resolves.
      // By the time a user types a description and clicks "Create", the probe
      // should have completed. If still null, default to tier 2.
      const caps = llm.capabilities.value
      const tier: 1 | 2 = caps ? selectTier(caps) : 2

      const content = await llm.complete(
        [{ role: 'user', content: buildPrompt(trimmed, format, tier) }],
        { maxTokens: tier === 1 ? 2048 : 512, temperature: 0.5 }
      )

      result.value = {
        content,
        filename: filenameFromDescription(trimmed, format),
        tier
      }
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Failed to generate draft.'
    } finally {
      generating.value = false
    }
  }

  return { generating, error, result, generate }
}
