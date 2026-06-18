<template>
  <div class="ai-draft-modal oc-p-m">
    <form class="ai-draft-modal__form" @submit.prevent="onSubmit">
      <div class="oc-mb-m">
        <label class="oc-label" for="ai-draft-description">
          {{ $gettext('Describe the document you need') }}
        </label>
        <textarea
          id="ai-draft-description"
          v-model="description"
          class="oc-input ai-draft-modal__textarea"
          :placeholder="$gettext('e.g. Q3 budget review for EMEA team, include agenda and action-items table')"
          rows="4"
          :disabled="generating"
          data-testid="draft-description"
        />
      </div>

      <div class="oc-mb-m">
        <label class="oc-label" for="ai-draft-format">
          {{ $gettext('Format') }}
        </label>
        <select
          id="ai-draft-format"
          v-model="format"
          class="oc-select ai-draft-modal__format"
          :disabled="generating"
          data-testid="draft-format"
        >
          <option value="markdown">{{ $gettext('Markdown') }}</option>
          <option value="text">{{ $gettext('Plain text') }}</option>
        </select>
      </div>

      <div v-if="error" class="oc-notification-danger oc-mb-m" role="alert" data-testid="draft-error">
        {{ error }}
      </div>

      <div v-if="result" class="oc-mb-m" data-testid="draft-preview">
        <label class="oc-label">{{ $gettext('Preview') }}</label>
        <pre class="ai-draft-modal__preview oc-p-s">{{ result.content }}</pre>
        <p class="oc-text-muted oc-mt-xs">
          {{ $gettext('Will be saved as: %{filename}', { filename: result.filename }) }}
          <span v-if="result.tier === 1" class="oc-badge" data-testid="tier-badge">
            {{ $gettext('Structured draft') }}
          </span>
          <span v-else class="oc-badge" data-testid="tier-badge">
            {{ $gettext('Narrative draft') }}
          </span>
        </p>
      </div>

      <div class="oc-flex oc-flex-right oc-gap-s">
        <button
          type="button"
          class="oc-button oc-button-secondary"
          :disabled="generating"
          data-testid="draft-cancel"
          @click="emit('cancel')"
        >
          {{ $gettext('Cancel') }}
        </button>

        <button
          v-if="!result"
          type="submit"
          class="oc-button oc-button-primary"
          :disabled="generating || !description.trim()"
          data-testid="draft-submit"
        >
          <span v-if="generating" data-testid="draft-spinner">{{ $gettext('Generating…') }}</span>
          <span v-else>{{ $gettext('Create draft') }}</span>
        </button>

        <button
          v-if="result"
          type="button"
          class="oc-button oc-button-primary"
          :disabled="saving"
          data-testid="draft-save"
          @click="onSave"
        >
          <span v-if="saving">{{ $gettext('Saving…') }}</span>
          <span v-else>{{ $gettext('Save to folder') }}</span>
        </button>
      </div>
    </form>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useGettext } from 'vue3-gettext'
import { useDraftCreator } from '../composables/useDraftCreator'
import type { DraftFormat } from '../composables/useDraftCreator'
import type { LLMConfig } from '../composables/useLLM'

interface Props {
  llmConfig: LLMConfig
  /** Opaque space object forwarded to the file-save callback. */
  space?: unknown
  /** Opaque current-folder resource forwarded to the file-save callback. */
  currentFolder?: unknown
  /** Called with (content, filename) when the user confirms the save. */
  onFileSave?: (content: string, filename: string, space: unknown, currentFolder: unknown) => Promise<void>
}

const props = withDefaults(defineProps<Props>(), {
  space: null,
  currentFolder: null,
  onFileSave: undefined
})

const emit = defineEmits<{
  cancel: []
  saved: [filename: string]
}>()

const { $gettext } = useGettext()

const description = ref('')
const format = ref<DraftFormat>('markdown')
const saving = ref(false)

const { generating, error, result, generate } = useDraftCreator(props.llmConfig)

async function onSubmit(): Promise<void> {
  await generate(description.value, format.value)
}

async function onSave(): Promise<void> {
  if (!result.value) return

  saving.value = true
  error.value = null
  try {
    if (props.onFileSave) {
      await props.onFileSave(
        result.value.content,
        result.value.filename,
        props.space,
        props.currentFolder
      )
    }
    emit('saved', result.value.filename)
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to save file.'
  } finally {
    saving.value = false
  }
}
</script>

<style scoped>
.ai-draft-modal__textarea {
  width: 100%;
  resize: vertical;
  min-height: 100px;
}

.ai-draft-modal__format {
  width: 100%;
}

.ai-draft-modal__preview {
  max-height: 200px;
  overflow-y: auto;
  white-space: pre-wrap;
  word-break: break-word;
  background: var(--oc-color-background-secondary, #f5f5f5);
  border-radius: 4px;
  font-size: 0.85em;
}
</style>
