import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  selectTier,
  filenameFromDescription,
  buildPrompt,
  useDraftCreator
} from '../../src/composables/useDraftCreator'
import type { LLMCapabilities } from '../../src/composables/useLLM'

// ── selectTier ────────────────────────────────────────────────────────────────

describe('selectTier', () => {
  it('returns tier 1 for large context windows', () => {
    const caps: LLMCapabilities = { structuredOutput: true, toolUse: true, contextTokens: 8192, streaming: true }
    expect(selectTier(caps)).toBe(1)
  })

  it('returns tier 1 when context tokens exceed minimum', () => {
    const caps: LLMCapabilities = { structuredOutput: false, toolUse: false, contextTokens: 32768, streaming: false }
    expect(selectTier(caps)).toBe(1)
  })

  it('returns tier 2 for small context windows', () => {
    const caps: LLMCapabilities = { structuredOutput: false, toolUse: false, contextTokens: 4096, streaming: false }
    expect(selectTier(caps)).toBe(2)
  })

  it('returns tier 2 at the boundary below 8192', () => {
    const caps: LLMCapabilities = { structuredOutput: true, toolUse: true, contextTokens: 8191, streaming: true }
    expect(selectTier(caps)).toBe(2)
  })
})

// ── filenameFromDescription ───────────────────────────────────────────────────

describe('filenameFromDescription', () => {
  it('produces a .md extension for markdown format', () => {
    expect(filenameFromDescription('Budget review', 'markdown')).toMatch(/\.md$/)
  })

  it('produces a .txt extension for text format', () => {
    expect(filenameFromDescription('Budget review', 'text')).toMatch(/\.txt$/)
  })

  it('slugifies the description', () => {
    expect(filenameFromDescription('Q3 Budget Review!', 'markdown')).toBe('q3-budget-review.md')
  })

  it('truncates long descriptions to 50 chars before extension', () => {
    const long = 'This is a very long description that should be truncated because it exceeds the limit'
    const filename = filenameFromDescription(long, 'text')
    const slug = filename.replace(/\.txt$/, '')
    expect(slug.length).toBeLessThanOrEqual(50)
  })

  it('falls back to "draft" when description is empty', () => {
    expect(filenameFromDescription('', 'markdown')).toBe('draft.md')
  })

  it('strips special characters', () => {
    // / and ! stripped; & stripped leaving two spaces which collapse to one dash
    expect(filenameFromDescription('hello/world & more!', 'markdown')).toBe('helloworld-more.md')
  })
})

// ── buildPrompt ───────────────────────────────────────────────────────────────

describe('buildPrompt', () => {
  it('tier-1 markdown prompt references Markdown formatting', () => {
    const prompt = buildPrompt('Meeting notes', 'markdown', 1)
    expect(prompt).toContain('Markdown')
  })

  it('tier-1 plain-text prompt references plain text', () => {
    const prompt = buildPrompt('Meeting notes', 'text', 1)
    expect(prompt).toContain('plain text')
  })

  it('tier-1 prompt includes description', () => {
    const prompt = buildPrompt('My custom description', 'markdown', 1)
    expect(prompt).toContain('My custom description')
  })

  it('tier-2 prompt is shorter than tier-1', () => {
    const t1 = buildPrompt('some description', 'markdown', 1)
    const t2 = buildPrompt('some description', 'markdown', 2)
    expect(t2.length).toBeLessThan(t1.length)
  })

  it('tier-2 prompt still includes description', () => {
    const prompt = buildPrompt('Incident report', 'text', 2)
    expect(prompt).toContain('Incident report')
  })

  it('tier-1 prompt instructs to use TODO placeholders', () => {
    const prompt = buildPrompt('Project brief', 'markdown', 1)
    expect(prompt.toLowerCase()).toContain('todo')
  })
})

// ── useDraftCreator ───────────────────────────────────────────────────────────

const FAKE_LLM_CONFIG = {
  endpoint: 'http://localhost:11434/v1',
  model: 'test-model'
}

describe('useDraftCreator', () => {
  beforeEach(() => {
    // Stub fetch so probing and generation don't hit the network.
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true,
      headers: { get: () => null },
      body: { cancel: vi.fn() },
      json: async () => ({
        choices: [{ message: { content: '# Draft\n\nContent here.' } }],
        data: [{ id: 'test-model', context_length: 16384 }]
      })
    }))
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('starts with generating=false', () => {
    const { generating } = useDraftCreator(FAKE_LLM_CONFIG)
    expect(generating.value).toBe(false)
  })

  it('starts with no error', () => {
    const { error } = useDraftCreator(FAKE_LLM_CONFIG)
    expect(error.value).toBeNull()
  })

  it('starts with no result', () => {
    const { result } = useDraftCreator(FAKE_LLM_CONFIG)
    expect(result.value).toBeNull()
  })

  it('sets error when description is empty', async () => {
    const { error, generate } = useDraftCreator(FAKE_LLM_CONFIG)
    await generate('', 'markdown')
    expect(error.value).toBeTruthy()
  })

  it('sets error when description is only whitespace', async () => {
    const { error, generate } = useDraftCreator(FAKE_LLM_CONFIG)
    await generate('   ', 'markdown')
    expect(error.value).toBeTruthy()
  })

  it('populates result after successful generation', async () => {
    const { result, generate } = useDraftCreator(FAKE_LLM_CONFIG)
    await generate('Meeting agenda', 'markdown')
    expect(result.value).not.toBeNull()
  })

  it('result contains a filename with correct extension for markdown', async () => {
    const { result, generate } = useDraftCreator(FAKE_LLM_CONFIG)
    await generate('Project brief', 'markdown')
    expect(result.value?.filename).toMatch(/\.md$/)
  })

  it('result contains a filename with correct extension for plain text', async () => {
    const { result, generate } = useDraftCreator(FAKE_LLM_CONFIG)
    await generate('Project brief', 'text')
    expect(result.value?.filename).toMatch(/\.txt$/)
  })

  it('clears previous result and error on new generate call', async () => {
    const { result, error, generate } = useDraftCreator(FAKE_LLM_CONFIG)
    await generate('First', 'markdown')
    expect(result.value).not.toBeNull()
    await generate('Second', 'text')
    expect(error.value).toBeNull()
  })

  it('sets error and clears result on LLM failure', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('Network error')))
    const { result, error, generate } = useDraftCreator(FAKE_LLM_CONFIG)
    await generate('Meeting notes', 'markdown')
    expect(error.value).toBeTruthy()
    expect(result.value).toBeNull()
  })
})
