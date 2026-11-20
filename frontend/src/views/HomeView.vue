<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import WalletCard from '../components/WalletCard.vue'
import { useWalletsStore } from '../stores/wallets'

const { t } = useI18n()
const router = useRouter()
const wallets = useWalletsStore()
</script>

<template>
  <section class="stack">
    <div class="heading">
      <div>
        <h2>{{ t('home.wallets') }}</h2>
        <p class="muted">{{ t('home.subtitle') }}</p>
      </div>
      <button class="primary" type="button" @click="router.push({ name: 'wizard' })">＋ {{ t('home.add') }}</button>
    </div>
    <WalletCard v-for="wallet in wallets.wallets" :key="wallet.id" :wallet="wallet" />
    <div v-if="!wallets.wallets.length" class="card empty">{{ t('home.empty') }}</div>
  </section>
</template>
