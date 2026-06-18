import { ref } from 'vue'
import {
  defineWebApplication,
  useModals
} from '@ownclouders/web-pkg'
import type { Action, ActionExtension, AppConfigObject, Extension } from '@ownclouders/web-pkg'
import { useGettext } from 'vue3-gettext'
import DraftModal from './components/DraftModal.vue'
import type { LLMConfig } from './composables/useLLM'
import { useLLM } from './composables/useLLM'

export default defineWebApplication({
  setup({ applicationConfig }) {
    const { $gettext } = useGettext()
    const { dispatchModal, removeModal } = useModals()

    const llmConfig = extractLLMConfig(applicationConfig)

    // Tier 3: LLM not configured — extension returns no extensions, so the
    // upload menu item is never registered (it stays hidden).
    if (!llmConfig) {
      return {}
    }

    // Initiate capability probe in the background so it's ready before first click.
    useLLM(llmConfig)

    const uploadMenuAction: Action = {
      name: 'ai-quick-draft-creator-draft',
      icon: 'pencil',
      label: () => $gettext('Draft from description'),
      isVisible: () => true,
      handler: (options) => {
        const opts = options as Record<string, unknown> | undefined
        const space = opts?.space ?? null
        const resources = opts?.resources
        const currentFolder = Array.isArray(resources) ? (resources[0] ?? null) : null

        let modalId = ''
        const close = () => removeModal(modalId)

        const { id } = dispatchModal({
          title: $gettext('Draft from description'),
          hideActions: true,
          customComponent: DraftModal,
          customComponentAttrs: () => ({
            llmConfig,
            space,
            currentFolder,
            onFileSave: async (
              content: string,
              filename: string,
              targetSpace: unknown,
              targetFolder: unknown
            ) => {
              await saveFileToFolder(content, filename, targetSpace, targetFolder)
              close()
            }
          }),
          onCancel: close
        })
        modalId = id
      }
    }

    const actionExt: ActionExtension = {
      id: 'ai-quick-draft-creator.upload-menu-action',
      type: 'action',
      extensionPointIds: ['app.files.upload-menu'],
      action: uploadMenuAction
    }

    const extensions = ref<Extension[]>([actionExt])

    return { extensions }
  }
})

/**
 * Saves draft content to the user's current folder via WebDAV PUT.
 * The space and folder are passed through opaquely from the upload-menu context.
 * Type assertions are unavoidable here: the web-client SpaceResource and Resource
 * types are not available as static imports in this standalone extension package.
 */
async function saveFileToFolder(
  content: string,
  filename: string,
  space: unknown,
  currentFolder: unknown
): Promise<void> {
  const spaceObj = space as { webDavPath?: string } | null
  const folderObj = currentFolder as { path?: string } | null

  const basePath = spaceObj?.webDavPath ?? ''
  const folderPath = folderObj?.path ?? '/'
  const sep = folderPath.endsWith('/') ? '' : '/'
  const url = `${basePath}${folderPath}${sep}${encodeURIComponent(filename)}`

  const response = await fetch(url, {
    method: 'PUT',
    credentials: 'include',
    headers: { 'Content-Type': 'text/plain; charset=utf-8' },
    body: content
  })

  if (!response.ok) {
    throw new Error(`Failed to save file: ${response.status} ${response.statusText}`)
  }
}

function extractLLMConfig(applicationConfig: AppConfigObject): LLMConfig | null {
  const llm: unknown = applicationConfig['llm']
  if (typeof llm !== 'object' || llm === null) return null
  const { endpoint, model, apiKey } = llm as Record<string, unknown>
  if (typeof endpoint !== 'string' || !endpoint) return null
  if (typeof model !== 'string' || !model) return null
  return {
    endpoint,
    model,
    apiKey: typeof apiKey === 'string' ? apiKey : undefined
  }
}
