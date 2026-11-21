<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '../../api/client'
import { useSessionStore } from '../../stores/session'
import { useUiStore } from '../../stores/ui'
import { useWalletsStore } from '../../stores/wallets'

const { t } = useI18n()
const session = useSessionStore()
const ui = useUiStore()
const wallets = useWalletsStore()

const confirmName = ref('')
const password = ref('')
const passphrase = ref('')

function clear(): void {
  confirmName.value = ''
  password.value = ''
  passphrase.value = ''
}

async function remove(): Promise<void> {
  const wallet = wallets.current
  if (!wallet) return
  if (confirmName.value !== wallet.name) {
    ui.notice = t('settings.delete.nameMismatch')
    return
  }
  try {
    await api.deleteWallet(wallet.id, confirmName.value, password.value, passphrase.value)
    wallets.remove(wallet.id)
    await session.init()
    ui.notice = t('settings.delete.deleted')
  } catch {
    ui.notice = t('settings.delete.failed')
  } finally {
    clear()
  }
}

watch(() => wallets.selectedId, clear)
onBeforeUnmount(clear)
</script>

<template>
  <article class="card stack">
    <h2>{{ t('settings.delete.title') }}</h2>
    <p v-if="wallets.current" class="muted">{{ wallets.current.name }}</p>
    <p class="warning">{{ t('settings.delete.warning') }}</p>
    <label for="delete-name">{{ t('settings.delete.typeName', { name: wallets.current?.name ?? '' }) }}</label>
    <input id="delete-name" v-model="confirmName" autocomplete="off">
    <label for="delete-password">{{ t('common.password') }}</label>
    <input id="delete-password" v-model="password" type="password" autocomplete="off">
    <label for="delete-passphrase">{{ t('common.passphraseIfSet') }}</label>
    <input id="delete-passphrase" v-model="passphrase" type="password" autocomplete="off">
    <button class="secondary" type="button" :disabled="!wallets.current || !password" @click="remove">
      {{ t('settings.delete.submit') }}
    </button>
  </article>
</template>
