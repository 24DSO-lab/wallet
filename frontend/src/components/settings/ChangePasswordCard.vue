<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '../../api/client'
import { MIN_PASSWORD_LENGTH } from '../../lib/validation'
import { useUiStore } from '../../stores/ui'

const { t } = useI18n()
const ui = useUiStore()

const oldPassword = ref('')
const newPassword = ref('')
const confirmation = ref('')

function clear(): void {
  oldPassword.value = ''
  newPassword.value = ''
  confirmation.value = ''
}

async function change(): Promise<void> {
  if (newPassword.value.length < MIN_PASSWORD_LENGTH || newPassword.value !== confirmation.value) {
    ui.notice = t('settings.password.invalidNew', { min: MIN_PASSWORD_LENGTH })
    return
  }
  try {
    await api.changePassword(oldPassword.value, newPassword.value)
    ui.notice = t('settings.password.changed')
  } catch {
    ui.notice = t('settings.password.failed')
  } finally {
    clear()
  }
}

onBeforeUnmount(clear)
</script>

<template>
  <article class="card stack">
    <h2>{{ t('settings.password.title') }}</h2>
    <label for="old-password">{{ t('settings.password.current') }}</label>
    <input id="old-password" v-model="oldPassword" type="password" autocomplete="off">
    <label for="new-password">{{ t('settings.password.new', { min: MIN_PASSWORD_LENGTH }) }}</label>
    <input id="new-password" v-model="newPassword" type="password" autocomplete="new-password">
    <label for="new-password-confirm">{{ t('settings.password.repeat') }}</label>
    <input id="new-password-confirm" v-model="confirmation" type="password" autocomplete="new-password">
    <button class="secondary" type="button" :disabled="!oldPassword || !newPassword || !confirmation" @click="change">
      {{ t('settings.password.submit') }}
    </button>
  </article>
</template>
