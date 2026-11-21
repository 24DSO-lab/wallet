<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import type { Balance } from '../api/types'
import { formatAmount } from '../lib/amount'
import { useDateFormat } from '../lib/locale'
import { useWalletsStore } from '../stores/wallets'
import AssetContract from './AssetContract.vue'

const props = defineProps<{ walletId: string; balance: Balance }>()

const { t } = useI18n()
const router = useRouter()
const wallets = useWalletsStore()
const { time } = useDateFormat()

function warningText(code: string): string {
  if (code.startsWith('refresh_failed')) {
    const reason = code.split(':')[1]
    return reason ? `${t('warning.refreshFailed')} (${reason})` : t('warning.refreshFailed')
  }
  if (code.startsWith('history_unavailable:')) return t('warning.historyUnavailable')
  if (code.startsWith('fee_balance_unavailable:')) return t('warning.feeBalanceUnavailable')
  return t('warning.partial')
}

function receive(): void {
  wallets.select(props.walletId)
  void router.push({ name: 'receive', params: { id: props.walletId, asset: props.balance.asset } })
}
</script>

<template>
  <div class="asset">
    <div class="asset-head">
      <b>{{ balance.asset }}</b>
      <small>{{ wallets.assetMeta(balance.asset)?.networkName ?? balance.network }}</small>
    </div>
    <strong v-if="balance.updatedAt" class="asset-balance">
      {{ formatAmount(balance.amount, balance.decimals) }} <span class="unit">{{ balance.asset }}</span>
    </strong>
    <strong v-else class="asset-balance stale">{{ t('asset.notUpdated') }}</strong>
    <small v-if="balance.pendingAmount && balance.pendingAmount !== '0'">
      {{ t('asset.pending') }}: {{ formatAmount(balance.pendingAmount, balance.decimals) }} {{ balance.asset }}
    </small>
    <small v-if="balance.feeAsset">
      {{ t('asset.feeBalance') }}:
      {{ formatAmount(balance.feeBalance ?? '0', balance.feeAsset === 'ETH' ? 18 : 6) }} {{ balance.feeAsset }}
    </small>
    <p v-for="code in wallets.warningsFor(walletId, balance.asset)" :key="code" class="warning" role="status">
      {{ warningText(code) }}
    </p>
    <AssetContract :asset="balance.asset" />
    <small v-if="balance.updatedAt">{{ t('asset.updatedAt', { time: time(balance.updatedAt) }) }}</small>
    <div class="asset-actions">
      <button class="secondary" type="button" @click="receive">{{ t('asset.receive') }}</button>
      <button
        class="secondary icon-btn"
        type="button"
        :disabled="wallets.isAssetRefreshing(walletId, balance.asset)"
        :aria-label="t('common.refresh')"
        :title="t('common.refresh')"
        :aria-busy="wallets.isAssetRefreshing(walletId, balance.asset)"
        @click="wallets.refreshAsset(walletId, balance.asset)"
      >
        {{ wallets.isAssetRefreshing(walletId, balance.asset) ? '…' : '↻' }}
      </button>
    </div>
  </div>
</template>
