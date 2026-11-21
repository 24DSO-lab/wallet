<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useWalletsStore } from '../../stores/wallets'

const { t } = useI18n()
const wallets = useWalletsStore()
</script>

<template>
  <article class="card stack">
    <h2>{{ t('settings.selected.title') }}</h2>
    <p class="muted">{{ t('settings.selected.hint') }}</p>
    <p v-if="!wallets.wallets.length" class="muted">{{ t('settings.selected.none') }}</p>
    <template v-else>
      <label for="settings-wallet">{{ t('settings.selected.label') }}</label>
      <select id="settings-wallet" :value="wallets.selectedId" @change="wallets.select(($event.target as HTMLSelectElement).value)">
        <option v-for="wallet in wallets.wallets" :key="wallet.id" :value="wallet.id">{{ wallet.name }}</option>
      </select>
    </template>
  </article>
</template>
