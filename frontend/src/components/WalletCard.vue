<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import type { Wallet } from '../api/types'
import { useWalletsStore } from '../stores/wallets'
import AssetTile from './AssetTile.vue'

const props = defineProps<{ wallet: Wallet }>()

const { t } = useI18n()
const router = useRouter()
const wallets = useWalletsStore()

function open(): void {
  wallets.select(props.wallet.id)
  void router.push({ name: 'wallet', params: { id: props.wallet.id } })
}
</script>

<template>
  <article class="card">
    <div class="heading">
      <h3>{{ wallet.name }}</h3>
      <div class="row">
        <button
          class="secondary"
          type="button"
          :disabled="wallets.refreshingId === wallet.id"
          @click="wallets.refreshWallet(wallet.id)"
        >
          {{ wallets.refreshingId === wallet.id ? t('common.refreshing') : t('home.refreshBalances') }}
        </button>
        <button class="quiet" type="button" @click="open">{{ t('home.open') }} →</button>
      </div>
    </div>
    <div class="assets">
      <AssetTile v-for="balance in wallet.balances" :key="balance.asset" :wallet-id="wallet.id" :balance="balance" />
    </div>
  </article>
</template>
