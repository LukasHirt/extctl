import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useSynthesizer, MIN_FILES, MAX_FILES } from '../../src/composables/useSynthesizer'

// Reset module-level state between tests by reimporting
vi.mock('../../src/composables/useLLM', () => ({
  useLLM: vi.fn(() => ({
    capabilities: { value: { structuredOutput: false, toolUse: false, contextTokens: 8192, streaming: false } },
    complete: vi.fn().mockResolvedValue('Summary text'),
    completeJSON: vi.fn(),
    stream: vi.fn().mockImplementation((_msgs: unknown, onChunk: (c: string) => void) => {
      onChunk('**Shared Themes**\n- theme1\n**Key Differences**\n- diff1\n**Action Items**\n- action1')
      return Promise.resolve()
    })
  }))
}))

const makeResource = (id: string, name: string, path = `/dav/spaces/abc/${name}`) => ({
  id,
  name,
  webDavPath: path
})

describe('useSynthesizer', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      statusText: 'OK',
      text: () => Promise.resolve('file content'),
      json: () => Promise.resolve({})
    })
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.clearAllMocks()
  })

  describe('openPanel / closePanel', () => {
    it('opens the panel with the given resources and config', () => {
      const { openPanel, panelOpen, pendingResources, llmConfig } = useSynthesizer()
      const config = { endpoint: 'http://llm.test/v1', model: 'gpt-4' }
      const resources = [makeResource('1', 'a.md'), makeResource('2', 'b.md')]

      openPanel(resources, config)

      expect(panelOpen.value).toBe(true)
      expect(pendingResources.value).toHaveLength(2)
      expect(llmConfig.value).toEqual(config)
    })

    it('clamps resources to MAX_FILES', () => {
      const { openPanel, pendingResources } = useSynthesizer()
      const config = { endpoint: 'http://llm.test/v1', model: 'gpt-4' }
      const resources = Array.from({ length: MAX_FILES + 3 }, (_, i) =>
        makeResource(String(i), `file${i}.md`)
      )

      openPanel(resources, config)

      expect(pendingResources.value).toHaveLength(MAX_FILES)
    })

    it('closes the panel', () => {
      const { openPanel, closePanel, panelOpen } = useSynthesizer()
      openPanel([makeResource('1', 'a.md'), makeResource('2', 'b.md')], {
        endpoint: 'http://llm.test/v1',
        model: 'gpt-4'
      })
      expect(panelOpen.value).toBe(true)

      closePanel()

      expect(panelOpen.value).toBe(false)
    })
  })

  describe('MIN_FILES / MAX_FILES constants', () => {
    it('MIN_FILES is 2', () => {
      expect(MIN_FILES).toBe(2)
    })

    it('MAX_FILES is 10', () => {
      expect(MAX_FILES).toBe(10)
    })
  })

  describe('fetchContent', () => {
    it('fetches file content via WebDAV GET', async () => {
      const { fetchContent } = useSynthesizer()
      const resource = makeResource('1', 'notes.md', 'http://ocis.test/dav/spaces/abc/notes.md')

      const content = await fetchContent(resource)

      expect(fetchMock).toHaveBeenCalledWith(
        'http://ocis.test/dav/spaces/abc/notes.md',
        expect.objectContaining({ credentials: 'include' })
      )
      expect(content).toBe('file content')
    })

    it('throws on HTTP error', async () => {
      fetchMock.mockResolvedValueOnce({ ok: false, status: 404, statusText: 'Not Found' })
      const { fetchContent } = useSynthesizer()
      const resource = makeResource('1', 'missing.md', 'http://ocis.test/dav/missing.md')

      await expect(fetchContent(resource)).rejects.toThrow('404')
    })
  })

  describe('synthesize', () => {
    it('streams output via onChunk callback', async () => {
      const { synthesize } = useSynthesizer()
      const config = { endpoint: 'http://llm.test/v1', model: 'gpt-4' }
      const resources = [makeResource('1', 'a.md'), makeResource('2', 'b.md')]
      const chunks: string[] = []

      await synthesize(resources, config, (chunk) => chunks.push(chunk))

      expect(chunks.length).toBeGreaterThan(0)
      const full = chunks.join('')
      expect(full).toContain('Shared Themes')
    })

    it('uses single-pass when estimated tokens fit in context (tier 1)', async () => {
      const { useLLM } = await import('../../src/composables/useLLM')
      const mockLLM = vi.mocked(useLLM)
      const streamSpy = vi.fn().mockResolvedValue(undefined)
      const completeSpy = vi.fn().mockResolvedValue('summary')
      mockLLM.mockReturnValueOnce({
        capabilities: { value: { structuredOutput: false, toolUse: false, contextTokens: 128000, streaming: true } },
        complete: completeSpy,
        completeJSON: vi.fn(),
        stream: streamSpy
      })

      const { synthesize } = useSynthesizer()
      // Short content — well within context window
      await synthesize(
        [makeResource('1', 'a.md'), makeResource('2', 'b.md')],
        { endpoint: 'http://llm.test/v1', model: 'gpt-4' },
        () => {}
      )

      // Single-pass: stream called once, complete not called for per-file summarization
      expect(streamSpy).toHaveBeenCalledTimes(1)
      expect(completeSpy).not.toHaveBeenCalled()
    })

    it('uses multi-pass when content exceeds context ratio (tier 2)', async () => {
      const { useLLM } = await import('../../src/composables/useLLM')
      const mockLLM = vi.mocked(useLLM)
      const streamSpy = vi.fn().mockResolvedValue(undefined)
      const completeSpy = vi.fn().mockResolvedValue('per-file summary')
      mockLLM.mockReturnValueOnce({
        capabilities: { value: { structuredOutput: false, toolUse: false, contextTokens: 100, streaming: false } },
        complete: completeSpy,
        completeJSON: vi.fn(),
        stream: streamSpy
      })

      // Large content that will exceed 60% of 100 tokens
      fetchMock.mockResolvedValue({
        ok: true,
        status: 200,
        statusText: 'OK',
        text: () => Promise.resolve('x'.repeat(500))
      })

      const { synthesize } = useSynthesizer()
      await synthesize(
        [makeResource('1', 'big-a.md'), makeResource('2', 'big-b.md')],
        { endpoint: 'http://llm.test/v1', model: 'small-model' },
        () => {}
      )

      // Multi-pass: complete called once per file, then stream for merge
      expect(completeSpy).toHaveBeenCalledTimes(2)
      expect(streamSpy).toHaveBeenCalledTimes(1)
    })
  })

  describe('saveAsMarkdown', () => {
    it('PUTs the content to the derived folder WebDAV path', async () => {
      fetchMock.mockResolvedValueOnce({ ok: true, status: 201, statusText: 'Created' })
      const { saveAsMarkdown } = useSynthesizer()

      await saveAsMarkdown('# Synthesis\n\nContent', 'synthesis-2026-06-19.md', 'http://ocis.test/dav/spaces/abc/folder')

      expect(fetchMock).toHaveBeenCalledWith(
        'http://ocis.test/dav/spaces/abc/folder/synthesis-2026-06-19.md',
        expect.objectContaining({
          method: 'PUT',
          credentials: 'include',
          body: '# Synthesis\n\nContent'
        })
      )
    })

    it('throws when save fails', async () => {
      fetchMock.mockResolvedValueOnce({ ok: false, status: 409, statusText: 'Conflict' })
      const { saveAsMarkdown } = useSynthesizer()

      await expect(
        saveAsMarkdown('content', 'file.md', 'http://ocis.test/dav/spaces/abc')
      ).rejects.toThrow('409')
    })
  })
})
