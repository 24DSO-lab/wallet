<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '../../api/client'
import { useSessionStore } from '../../stores/session'
import { useUiStore } from '../../stores/ui'

const { t } = useI18n()
const session = useSessionStore()
const ui = useUiStore()
const password = ref('')

async function backup(): Promise<void> {
  try {
    const created = await api.backup(password.value)
    ui.notice = created ? t('settings.backup.created') : t('settings.backup.cancelled')
  } catch {
    ui.notice = t('settings.backup.failed')
  } finally {
    password.value = ''
  }
}

async function restore(): Promise<void> {
  try {
    const restored = await api.restoreBackup(password.value)
    if (restored) {
      await session.init()
      ui.notice = t('settings.backup.restored')
    } else {
      ui.notice = t('settings.backup.restoreCancelled')
    }
  } catch {
    ui.notice = t('settings.backup.restoreFailed')
  } finally {
    password.value = ''
  }
}

onBeforeUnmount(() => {
  password.value = ''
})
</script>

<template>
  <article class="card stack">
    <h2>{{ t('settings.backup.title') }}</h2>
    <label for="backup-password">{{ t('settings.backup.password') }}</label>
    <input id="backup-password" v-model="password" type="password" autocomplete="off">
    <div class="row">
      <button class="secondary" type="button" :disabled="!password" @click="backup">{{ t('settings.backup.create') }}</button>
      <button class="secondary" type="button" :disabled="!password" @click="restore">{{ t('settings.backup.restore') }}</button>
    </div>
    <p class="muted">{{ t('settings.backup.hint') }}</p>
  </article>
</template>
