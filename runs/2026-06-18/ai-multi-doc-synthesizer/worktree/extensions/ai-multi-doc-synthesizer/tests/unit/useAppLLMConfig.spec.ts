import { describe, it, expect, vi } from 'vitest'
import { defineComponent, provide, h } from 'vue'
import { mount } from '@vue/test-utils'
import { useAppLLMConfig } from '../../src/composables/useAppLLMConfig'

function mountWithConfig(config: unknown) {
  let result: ReturnType<typeof useAppLLMConfig> | undefined

  const Child = defineComponent({
    setup() {
      result = useAppLLMConfig()
      return () => h('div')
    }
  })

  const Parent = defineComponent({
    setup() {
      provide('$config', config)
      return () => h(Child)
    }
  })

  mount(Parent)
  return result
}

describe('useAppLLMConfig', () => {
  it('returns null when no config is injected', () => {
    let result: ReturnType<typeof useAppLLMConfig> | undefined
    const Comp = defineComponent({
      setup() {
        result = useAppLLMConfig()
        return () => h('div')
      }
    })
    mount(Comp)
    expect(result).toBeNull()
  })

  it('returns null when endpoint is missing', () => {
    const result = mountWithConfig({ options: { llmModel: 'gpt-4' } })
    expect(result).toBeNull()
  })

  it('returns null when model is missing', () => {
    const result = mountWithConfig({ options: { llmEndpoint: 'http://llm.test/v1' } })
    expect(result).toBeNull()
  })

  it('returns null when options is not an object', () => {
    const result = mountWithConfig({ options: 'invalid' })
    expect(result).toBeNull()
  })

  it('returns a valid LLMConfig when endpoint and model are present', () => {
    const result = mountWithConfig({
      options: { llmEndpoint: 'http://llm.test/v1', llmModel: 'gpt-4' }
    })
    expect(result).toEqual({ endpoint: 'http://llm.test/v1', model: 'gpt-4', apiKey: undefined })
  })

  it('includes apiKey when provided', () => {
    const result = mountWithConfig({
      options: { llmEndpoint: 'http://llm.test/v1', llmModel: 'gpt-4', llmApiKey: 'sk-test' }
    })
    expect(result).toEqual({ endpoint: 'http://llm.test/v1', model: 'gpt-4', apiKey: 'sk-test' })
  })

  it('omits apiKey when it is an empty string', () => {
    const result = mountWithConfig({
      options: { llmEndpoint: 'http://llm.test/v1', llmModel: 'gpt-4', llmApiKey: '' }
    })
    expect(result?.apiKey).toBeUndefined()
  })
})

// Suppress vue warning about missing provide in tests
vi.stubGlobal('console', { ...console, warn: vi.fn() })
