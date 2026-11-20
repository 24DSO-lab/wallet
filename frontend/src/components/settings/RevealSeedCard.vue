<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '../../api/client'
import { useUiStore } from '../../stores/ui'
import { useWalletsStore } from '../../stores/wallets'

const { t } = useI18n()
const ui = useUiStore()
const wallets = useWalletsStore()

const password = ref('')
const passphrase = ref('')
const revealed = ref('')

function clear(): void {
  password.value = ''
  passphrase.value = ''
  revealed.value = ''
}

async function reveal(): Promise<void> {
  if (!wallets.current) return
  try {
    revealed.value = await api.revealSeed(wallets.current.id, password.value, passphrase.value)
  } catch {
    ui.notice = t('settings.reveal.failed')
  } finally {
    password.value = ''
    passphrase.value = ''
  }
}

// The phrase must never outlive the settings screen.
onBeforeUnmount(clear)
</script>

<template>
  <article class="card stack">
    <h2>{{ t('settings.reveal.title') }}</h2>
    <p class="warning">{{ t('settings.reveal.warning') }}</p>
    <label for="reveal-password">{{ t('common.password') }}</label>
    <input id="reveal-password" v-model="password" type="password" autocomplete="off">
    <label for="reveal-passphrase">{{ t('common.passphraseIfSet') }}</label>
    <input id="reveal-passphrase" v-model="passphrase" type="password" autocomplete="off">
    <button class="secondary" type="button" :disabled="!wallets.current || !password" @click="reveal">
      {{ t('settings.reveal.show') }}
    </button>
    <p v-if="revealed" class="seed">{{ revealed }}</p>
    <button v-if="revealed" class="quiet" type="button" @click="revealed = ''">{{ t('settings.reveal.hide') }}</button>
  </article>
</template>
