<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { errorCode, sendErrorKey } from '../lib/errors'
import { checkSendAmount } from '../lib/validation'
import { useSendStore } from '../stores/send'
import { SENDABLE_ASSETS, useWalletsStore } from '../stores/wallets'

/** Used only when the wallet has no balance row for the asset yet. */
const DEFAULT_DECIMALS: Record<string, number> = { BTC: 8, ETH: 18, USDC: 6, USDT: 6 }

const { t } = useI18n()
const router = useRouter()
const send = useSendStore()
const wallets = useWalletsStore()
const busy = ref(false)
const error = ref('')

function decimalsFor(asset: string): number {
  const balance = wallets.current?.balances.find(item => item.asset === asset)
  return balance?.decimals ?? DEFAULT_DECIMALS[asset] ?? 8
}

/** Returns an error message, or an empty string when the form looks valid. */
function validate(): string {
  if (!send.recipient.trim()) return t('send.recipientRequired')
  const decimals = decimalsFor(send.asset)
  const check = checkSendAmount(send.amount, decimals)
  if (check.ok) {
    send.amount = check.value
    return ''
  }
  if (check.reason === 'zero') return t('send.amountZero')
  if (check.reason === 'precision') return t('send.amountPrecision', { asset: send.asset, decimals })
  return t('send.amountInvalid')
}

async function prepare(): Promise<void> {
  error.value = validate()
  if (error.value) return
  send.recipient = send.recipient.trim()
  busy.value = true
  try {
    await send.prepare(wallets.selectedId)
    await router.push({ name: 'review' })
  } catch (e) {
    const key = sendErrorKey(e, '')
    error.value = key ? t(key) : `${t('send.invalid')} (${errorCode(e)})`
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <section class="card narrow stack">
    <h2>{{ t('send.title') }}</h2>
    <p v-if="wallets.current" class="muted">{{ wallets.current.name }}</p>
    <form class="stack" novalidate @submit.prevent="prepare">
      <label for="send-asset">{{ t('send.asset') }}</label>
      <select id="send-asset" v-model="send.asset">
        <option v-for="asset in SENDABLE_ASSETS" :key="asset" :value="asset">{{ asset }}</option>
      </select>
      <label for="send-recipient">{{ t('send.recipient') }}</label>
      <input id="send-recipient" v-model="send.recipient" autocomplete="off" spellcheck="false">
      <label for="send-amount">{{ t('send.amount') }}</label>
      <input id="send-amount" v-model="send.amount" inputmode="decimal" autocomplete="off">
      <p v-if="error" class="error" role="alert">{{ error }}</p>
      <button class="primary" type="submit" :disabled="busy">{{ t('send.check') }}</button>
    </form>
  </section>
</template>
