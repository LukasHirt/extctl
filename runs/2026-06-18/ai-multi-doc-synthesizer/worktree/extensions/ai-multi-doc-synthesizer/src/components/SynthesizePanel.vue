<template>
  <Teleport to="body">
    <div
      v-if="panelOpen"
      class="synth-overlay"
      data-testid="synth-overlay"
      @click.self="closePanel"
    >
      <div
        class="synth-panel"
        role="dialog"
        aria-modal="true"
        aria-labelledby="synth-title"
        data-testid="synth-panel"
      >
        <div class="synth-header">
          <h2 id="synth-title" class="synth-title">Synthesize Documents</h2>
          <button
            class="synth-close"
            aria-label="Close"
            data-testid="synth-close"
            @click="closePanel"
          >
            ×
          </button>
        </div>

        <ul class="synth-file-list" data-testid="synth-file-list">
          <li
            v-for="r in pendingResources"
            :key="r.id"
            class="synth-file-item"
          >
            {{ r.name }}
          </li>
        </ul>

        <div v-if="status === 'idle'" class="synth-idle">
          <button
            class="synth-btn synth-btn--primary"
            data-testid="synth-start-btn"
            @click="startSynthesis"
          >
            Synthesize
          </button>
        </div>

        <div v-if="status === 'running'" class="synth-loading" data-testid="synth-loading">
          <span class="synth-spinner" aria-hidden="true" />
          Synthesizing…
        </div>

        <div
          v-if="errorMessage"
          class="synth-error"
          role="alert"
          data-testid="synth-error"
        >
          {{ errorMessage }}
        </div>

        <div v-if="output" class="synth-result" data-testid="synth-result">
          <div class="synth-output" data-testid="synth-output">{{ output }}</div>
          <div class="synth-result-actions">
            <button
              class="synth-btn"
              data-testid="synth-copy-btn"
              @click="copyOutput"
            >
              {{ copied ? 'Copied!' : 'Copy' }}
            </button>
            <button
              class="synth-btn"
              :disabled="saving"
              data-testid="synth-save-btn"
              @click="saveOutput"
            >
              {{ saving ? 'Saving…' : 'Save as Markdown' }}
            </button>
          </div>
          <div
            v-if="savedFilename"
            class="synth-saved"
            data-testid="synth-saved"
          >
            Saved as {{ savedFilename }}
          </div>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useSynthesizer } from '../composables/useSynthesizer'

type PanelStatus = 'idle' | 'running' | 'done'

const { panelOpen, pendingResources, llmConfig, closePanel, synthesize, saveAsMarkdown } =
  useSynthesizer()

const status = ref<PanelStatus>('idle')
const output = ref('')
const errorMessage = ref('')
const copied = ref(false)
const saving = ref(false)
const savedFilename = ref('')

watch(panelOpen, (isOpen) => {
  if (isOpen) {
    status.value = 'idle'
    output.value = ''
    errorMessage.value = ''
    copied.value = false
    saving.value = false
    savedFilename.value = ''
    void startSynthesis()
  }
})

async function startSynthesis(): Promise<void> {
  if (!llmConfig.value || status.value === 'running') return
  status.value = 'running'
  output.value = ''
  errorMessage.value = ''

  try {
    await synthesize([...pendingResources.value], llmConfig.value, (chunk) => {
      output.value += chunk
    })
    status.value = 'done'
  } catch (e) {
    errorMessage.value = e instanceof Error ? e.message : 'Synthesis failed'
    status.value = 'idle'
  }
}

async function copyOutput(): Promise<void> {
  try {
    await navigator.clipboard.writeText(output.value)
    copied.value = true
    setTimeout(() => {
      copied.value = false
    }, 2000)
  } catch {
    errorMessage.value = 'Failed to copy to clipboard'
  }
}

async function saveOutput(): Promise<void> {
  if (!pendingResources.value.length || saving.value) return
  saving.value = true
  errorMessage.value = ''
  savedFilename.value = ''

  try {
    const first = pendingResources.value[0]
    const folderPath = first.webDavPath.substring(0, first.webDavPath.lastIndexOf('/'))
    const filename = `synthesis-${new Date().toISOString().split('T')[0]}.md`
    await saveAsMarkdown(output.value, filename, folderPath)
    savedFilename.value = filename
  } catch (e) {
    errorMessage.value = e instanceof Error ? e.message : 'Failed to save file'
  } finally {
    saving.value = false
  }
}
</script>

<style scoped>
.synth-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 9999;
}

.synth-panel {
  background: #fff;
  border-radius: 8px;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.2);
  width: min(680px, 92vw);
  max-height: 80vh;
  overflow-y: auto;
  padding: 24px;
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.synth-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.synth-title {
  font-size: 1.2rem;
  font-weight: 600;
  margin: 0;
}

.synth-close {
  background: none;
  border: none;
  font-size: 1.5rem;
  cursor: pointer;
  padding: 4px 8px;
  border-radius: 4px;
  line-height: 1;
  color: #666;
}

.synth-close:hover {
  background: #f0f0f0;
}

.synth-file-list {
  list-style: none;
  margin: 0;
  padding: 8px 12px;
  background: #f7f7f7;
  border-radius: 6px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.synth-file-item {
  font-size: 0.875rem;
  color: #444;
}

.synth-file-item::before {
  content: '📄 ';
}

.synth-idle {
  display: flex;
  justify-content: flex-end;
}

.synth-loading {
  display: flex;
  align-items: center;
  gap: 8px;
  color: #555;
}

.synth-spinner {
  display: inline-block;
  width: 16px;
  height: 16px;
  border: 2px solid #ccc;
  border-top-color: #0078d4;
  border-radius: 50%;
  animation: synth-spin 0.8s linear infinite;
}

@keyframes synth-spin {
  to {
    transform: rotate(360deg);
  }
}

.synth-error {
  color: #c00;
  background: #fff0f0;
  border: 1px solid #fcc;
  border-radius: 4px;
  padding: 8px 12px;
  font-size: 0.875rem;
}

.synth-output {
  white-space: pre-wrap;
  font-size: 0.9rem;
  line-height: 1.6;
  background: #fafafa;
  border: 1px solid #e0e0e0;
  border-radius: 6px;
  padding: 16px;
  max-height: 320px;
  overflow-y: auto;
}

.synth-result-actions {
  display: flex;
  gap: 8px;
  justify-content: flex-end;
}

.synth-saved {
  font-size: 0.8rem;
  color: #080;
  text-align: right;
}

.synth-btn {
  background: #f0f0f0;
  border: 1px solid #ccc;
  border-radius: 4px;
  cursor: pointer;
  font-size: 0.875rem;
  padding: 6px 14px;
}

.synth-btn:hover:not(:disabled) {
  background: #e0e0e0;
}

.synth-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.synth-btn--primary {
  background: #0078d4;
  border-color: #0078d4;
  color: #fff;
}

.synth-btn--primary:hover:not(:disabled) {
  background: #006bbf;
}
</style>
