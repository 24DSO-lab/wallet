<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { api } from '../api/client'
import type { Tx, WalletAsset } from '../api/types'
import { useDateFormat } from '../lib/locale'
import { useSendStore } from '../stores/send'
import { useUiStore } from '../stores/ui'
import { SENDABLE_ASSETS, useWalletsStore } from '../stores/wallets'

const { t } = useI18n()
const router = useRouter()
const send = useSendStore()
const ui = useUiStore()
const wallets = useWalletsStore()
const { dateTime } = useDateFormat()

const history = ref<Tx[]>([])
const filter = ref<WalletAsset | 'ALL'>('ALL')
const busy = ref(false)

async function load(): Promise<void> {
  if (!wallets.selectedId) return
  try {
    history.value = await api.history(wallets.selectedId, filter.value === 'ALL' ? undefined : filter.value)
  } catch {
    // A failed reload keeps the previous list; the feedback area may already
    // carry a more specific message (for example after an unknown broadcast).
  }
}

async function replace(tx: Tx): Promise<void> {
  busy.value = true
  try {
    await send.prepareRBF(wallets.selectedId, tx.id)
    await router.push({ name: 'review' })
  } catch {
    ui.error = t('history.rbfFailed')
  } finally {
    busy.value = false
  }
}

function directionLabel(direction: Tx['direction']): string {
  if (direction === 'self') return t('history.self')
  return direction === 'in' ? t('history.in') : t('history.out')
}

onMounted(load)
</script>

<template>
  <section class="stack">
    <div class="heading">
      <h2>{{ t('history.title') }}</h2>
      <select v-model="filter" :aria-label="t('history.filter')" @change="load">
        <option value="ALL">{{ t('history.allAssets') }}</option>
        <option v-for="asset in SENDABLE_ASSETS" :key="asset" :value="asset">{{ asset }}</option>
      </select>
    </div>
    <article class="card">
      <p v-if="!history.length" class="muted">{{ t('history.empty') }}</p>
      <div v-for="tx in history" :key="tx.id" class="tx">
        <b>{{ tx.asset }} · {{ directionLabel(tx.direction) }}</b>
        <span>{{ tx.amount }} · {{ tx.status }}</span>
        <small v-if="tx.timestamp">{{ dateTime(tx.timestamp) }}</small>
        <small v-else class="muted">{{ t('history.timeUnknown') }}</small>
        <small class="break">TXID: {{ tx.id }}</small>
        <small v-if="tx.address" class="break">{{ t('history.address') }}: {{ tx.address }}</small>
        <small v-if="tx.feeAsset && tx.fee && tx.fee !== '0'">{{ t('history.fee') }}: {{ tx.fee }} {{ tx.feeAsset }}</small>
        <small v-else-if="tx.direction !== 'in' && !tx.feeAsset" class="muted">{{ t('history.feeUnknown') }}</small>
        <a v-if="tx.explorerUrl" :href="tx.explorerUrl" target="_blank" rel="noopener noreferrer">{{ t('history.openExplorer') }}</a>
        <button v-if="tx.canReplace" class="secondary" type="button" :disabled="busy" @click="replace(tx)">
          {{ t('history.speedUp') }}
        </button>
      </div>
    </article>
  </section>
</template>
