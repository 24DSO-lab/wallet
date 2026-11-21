<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '../../api/client'
import { useClipboard } from '../../lib/clipboard'

const { t } = useI18n()
const { copyText } = useClipboard()
const path = ref('')
const loaded = ref(false)

onMounted(async () => {
  try {
    path.value = await api.logPath()
  } catch {
    path.value = ''
  } finally {
    loaded.value = true
  }
})
</script>

<template>
  <article class="card stack">
    <h2>{{ t('settings.log.title') }}</h2>
    <p class="muted">{{ t('settings.log.hint') }}</p>
    <template v-if="path">
      <code class="address">{{ path }}</code>
      <div>
        <button class="secondary" type="button" @click="copyText(path)">{{ t('settings.log.copy') }}</button>
      </div>
    </template>
    <p v-else-if="loaded" class="muted">{{ t('settings.log.unavailable') }}</p>
  </article>
</template>
