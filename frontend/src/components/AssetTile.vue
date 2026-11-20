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
  if (code === 'refresh_failed') return t('warning.refreshFailed')
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
    <small>{{ wallets.assetMeta(balance.asset)?.networkName ?? balance.network }}</small>
    <b>{{ balance.asset }}</b>
    <AssetContract :asset="balance.asset" />
    <strong v-if="balance.updatedAt">
      {{ formatAmount(balance.amount, balance.decimals) }} <small>{{ balance.asset }}</small>
    </strong>
    <strong v-else>{{ t('asset.notUpdated') }}</strong>
    <small v-if="balance.pendingAmount && balance.pendingAmount !== '0'" class="muted">
      {{ t('asset.pending') }}: {{ formatAmount(balance.pendingAmount, balance.decimals) }} {{ balance.asset }}
    </small>
    <small v-if="balance.feeAsset" class="muted">
      {{ t('asset.feeBalance') }}:
      {{ formatAmount(balance.feeBalance ?? '0', balance.feeAsset === 'ETH' ? 18 : 6) }} {{ balance.feeAsset }}
    </small>
    <p v-for="code in wallets.warningsFor(walletId, balance.asset)" :key="code" class="warning" role="status">
      {{ warningText(code) }}
    </p>
    <small v-if="balance.updatedAt" class="muted">{{ t('asset.updatedAt', { time: time(balance.updatedAt) }) }}</small>
    <div class="row">
      <button
        class="secondary"
        type="button"
        :disabled="wallets.isAssetRefreshing(walletId, balance.asset)"
        @click="wallets.refreshAsset(walletId, balance.asset)"
      >
        {{ wallets.isAssetRefreshing(walletId, balance.asset) ? t('common.refreshing') : t('common.refresh') }}
      </button>
      <button class="secondary" type="button" @click="receive">{{ t('asset.receive') }}</button>
    </div>
  </div>
</template>
