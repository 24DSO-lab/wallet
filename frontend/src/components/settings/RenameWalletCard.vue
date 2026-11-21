<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '../../api/client'
import { useUiStore } from '../../stores/ui'
import { useWalletsStore } from '../../stores/wallets'

const { t } = useI18n()
const ui = useUiStore()
const wallets = useWalletsStore()
const newName = ref('')

watch(() => wallets.selectedId, () => {
  newName.value = ''
})

async function rename(): Promise<void> {
  const name = newName.value.trim()
  if (!wallets.current || !name) return
  try {
    await api.rename(wallets.current.id, name)
    await wallets.load()
    newName.value = ''
    ui.notice = t('settings.manage.renamed')
  } catch {
    ui.notice = t('settings.manage.renameFailed')
  }
}
</script>

<template>
  <article class="card stack">
    <h2>{{ t('settings.manage.title') }}</h2>
    <p v-if="wallets.current" class="muted">{{ wallets.current.name }}</p>
    <input v-model="newName" :placeholder="t('settings.manage.newName')" :aria-label="t('settings.manage.newName')">
    <button class="secondary" type="button" :disabled="!wallets.current || !newName.trim()" @click="rename">
      {{ t('settings.manage.rename') }}
    </button>
  </article>
</template>
