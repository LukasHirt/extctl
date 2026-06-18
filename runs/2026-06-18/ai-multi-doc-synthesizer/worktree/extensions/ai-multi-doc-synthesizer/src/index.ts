import { ref } from 'vue'
import { defineWebApplication, useExtensionRegistry } from '@ownclouders/web-pkg'
import type { ActionExtension, Extension, FileAction, FileActionOptions } from '@ownclouders/web-pkg'
import { useGettext } from 'vue3-gettext'
import App from './App.vue'
import { useSynthesizer, MIN_FILES, MAX_FILES } from './composables/useSynthesizer'
import { useAppLLMConfig } from './composables/useAppLLMConfig'

export default defineWebApplication({
  setup() {
    const { $gettext } = useGettext()
    const llmConfig = useAppLLMConfig()
    const { openPanel } = useSynthesizer()
    const registry = useExtensionRegistry()

    // Tier 3 degradation: when no LLM is configured, register no extensions.
    // The "Synthesize" button is absent from the batch bar.
    if (llmConfig !== null) {
      const action: FileAction = {
        name: 'ai-multi-doc-synthesizer',
        icon: 'article',
        label: (_options?: FileActionOptions): string => $gettext('Synthesize'),
        isVisible: (options?: FileActionOptions): boolean => {
          const eligible = options?.resources?.filter((r) => Boolean(r.webDavPath)) ?? []
          return eligible.length >= MIN_FILES && eligible.length <= MAX_FILES
        },
        handler: (options?: FileActionOptions): void => {
          const eligible = (options?.resources ?? [])
            .filter((r): r is typeof r & { webDavPath: string } => Boolean(r.webDavPath))
            .map((r) => ({
              id: r.id,
              name: r.name ?? r.path.split('/').filter(Boolean).pop() ?? r.id,
              webDavPath: r.webDavPath
            }))
          if (eligible.length >= MIN_FILES) {
            // llmConfig is non-null here (checked in outer if)
            openPanel(eligible, llmConfig)
          }
        }
      }

      const batchActionExt: ActionExtension = {
        id: 'ai-multi-doc-synthesizer.batch-action',
        type: 'action',
        extensionPointIds: ['global.files.batch-actions'],
        action
      }
      const extensions = ref<Extension[]>([batchActionExt])

      registry.registerExtensions(extensions)
    }

    return {}
  },

  rootComponent: App
})
