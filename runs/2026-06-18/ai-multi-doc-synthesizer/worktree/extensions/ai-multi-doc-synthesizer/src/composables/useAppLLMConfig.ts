import { inject } from 'vue'
import type { LLMConfig } from './useLLM'

// oCIS Web injects each extension's admin-configured options under '$config'.
// The admin sets these in oCIS server Application Configuration for this app.
interface AppOptions {
  options?: Record<string, unknown>
}

export function useAppLLMConfig(): LLMConfig | null {
  const config = inject<AppOptions | null>('$config', null)
  if (config === null) return null

  const opts = config.options
  if (typeof opts !== 'object' || opts === null) return null

  const endpoint = opts['llmEndpoint']
  const model = opts['llmModel']

  if (typeof endpoint !== 'string' || !endpoint || typeof model !== 'string' || !model) {
    return null
  }

  const apiKey = opts['llmApiKey']
  return {
    endpoint,
    model,
    apiKey: typeof apiKey === 'string' && apiKey ? apiKey : undefined
  }
}
