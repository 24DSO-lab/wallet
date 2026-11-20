<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useSendStore } from '../stores/send'
import { useUiStore } from '../stores/ui'
import { SENDABLE_ASSETS, useWalletsStore } from '../stores/wallets'

const { t } = useI18n()
const router = useRouter()
const send = useSendStore()
const ui = useUiStore()
const wallets = useWalletsStore()
const busy = ref(false)

async function prepare(): Promise<void> {
  busy.value = true
  try {
    await send.prepare(wallets.selectedId)
    await router.push({ name: 'review' })
  } catch (e) {
    ui.error = String(e).includes('duplicate_send') ? t('send.duplicate') : t('send.invalid')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <section class="card narrow stack">
    <h2>{{ t('send.title') }}</h2>
    <form class="stack" @submit.prevent="prepare">
      <label for="send-asset">{{ t('send.asset') }}</label>
      <select id="send-asset" v-model="send.asset">
        <option v-for="asset in SENDABLE_ASSETS" :key="asset" :value="asset">{{ asset }}</option>
      </select>
      <label for="send-recipient">{{ t('send.recipient') }}</label>
      <input id="send-recipient" v-model="send.recipient" autocomplete="off" spellcheck="false">
      <label for="send-amount">{{ t('send.amount') }}</label>
      <input id="send-amount" v-model="send.amount" inputmode="decimal" autocomplete="off">
      <button class="primary" type="submit" :disabled="busy">{{ t('send.check') }}</button>
    </form>
  </section>
</template>
