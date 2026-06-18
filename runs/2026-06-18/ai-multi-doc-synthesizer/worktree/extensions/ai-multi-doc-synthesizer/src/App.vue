<template>
  <div class="ai-multi-doc-synthesizer">
    <SynthesizePanel />
    <slot />
  </div>
</template>

<script setup lang="ts">
import SynthesizePanel from './components/SynthesizePanel.vue'
</script>
