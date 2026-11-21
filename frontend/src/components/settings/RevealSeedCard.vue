<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '../../api/client'
import { errorCode } from '../../lib/errors'
import { useWalletsStore } from '../../stores/wallets'

const { t } = useI18n()
const wallets = useWalletsStore()

const password = ref('')
const passphrase = ref('')
const revealed = ref('')
const error = ref('')

function clear(): void {
  password.value = ''
  passphrase.value = ''
  revealed.value = ''
  error.value = ''
}

async function reveal(): Promise<void> {
  if (!wallets.current) return
  error.value = ''
  revealed.value = ''
  try {
    revealed.value = await api.revealSeed(wallets.current.id, password.value, passphrase.value)
  } catch (e) {
    const code = errorCode(e)
    globalThis.console.error('Reveal seed failed:', code)
    if (code === 'wrong_password') error.value = t('settings.reveal.wrongPassword')
    else if (code === 'locked' || code === 'session_locked') error.value = t('send.errors.locked')
    else error.value = `${t('settings.reveal.failed')} (${code})`
  } finally {
    password.value = ''
    passphrase.value = ''
  }
}

// Never keep a phrase on screen for a wallet that is no longer the selected one.
watch(() => wallets.selectedId, clear)

// The phrase must never outlive the settings screen.
onBeforeUnmount(clear)
</script>

<template>
  <article class="card stack">
    <h2>{{ t('settings.reveal.title') }}</h2>
    <p v-if="wallets.current" class="muted">{{ wallets.current.name }}</p>
    <p class="warning">{{ t('settings.reveal.warning') }}</p>
    <label for="reveal-password">{{ t('common.password') }}</label>
    <input id="reveal-password" v-model="password" type="password" autocomplete="off">
    <label for="reveal-passphrase">{{ t('common.passphraseIfSet') }}</label>
    <input id="reveal-passphrase" v-model="passphrase" type="password" autocomplete="off">
    <button class="secondary" type="button" :disabled="!wallets.current || !password" @click="reveal">
      {{ t('settings.reveal.show') }}
    </button>
    <p v-if="error" class="error" role="alert">{{ error }}</p>
    <p v-if="revealed" class="seed">{{ revealed }}</p>
    <button v-if="revealed" class="quiet" type="button" @click="revealed = ''">{{ t('settings.reveal.hide') }}</button>
  </article>
</template>
