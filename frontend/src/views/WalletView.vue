<script setup lang="ts">
import { computed, watchEffect } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import AssetContract from '../components/AssetContract.vue'
import { formatAmount } from '../lib/amount'
import { useWalletsStore } from '../stores/wallets'

const props = defineProps<{ id: string }>()

const { t } = useI18n()
const router = useRouter()
const wallets = useWalletsStore()
const wallet = computed(() => wallets.wallets.find(item => item.id === props.id))

watchEffect(() => {
  if (wallet.value) wallets.select(wallet.value.id)
})

function receive(asset: string): void {
  void router.push({ name: 'receive', params: { id: props.id, asset } })
}
</script>

<template>
  <section v-if="wallet" class="stack">
    <div class="heading">
      <h2>{{ wallet.name }}</h2>
      <button class="primary" type="button" @click="router.push({ name: 'send' })">{{ t('wallet.send') }}</button>
    </div>
    <div class="assets">
      <article v-for="balance in wallet.balances" :key="balance.asset" class="card asset">
        <div class="asset-head">
          <h3>{{ balance.asset }}</h3>
          <small>{{ wallets.assetMeta(balance.asset)?.networkName ?? balance.network }}</small>
        </div>
        <strong v-if="balance.updatedAt" class="asset-balance">
          {{ formatAmount(balance.amount, balance.decimals) }} <span class="unit">{{ balance.asset }}</span>
        </strong>
        <strong v-else class="asset-balance stale">{{ t('asset.notUpdated') }}</strong>
        <AssetContract :asset="balance.asset" />
        <button class="secondary" type="button" @click="receive(balance.asset)">
          {{ t('wallet.receive', { asset: balance.asset }) }}
        </button>
      </article>
    </div>
  </section>
  <p v-else class="card empty">{{ t('wallet.notFound') }}</p>
</template>
