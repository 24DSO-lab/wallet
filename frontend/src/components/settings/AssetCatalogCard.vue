<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useClipboard } from '../../lib/clipboard'
import { useWalletsStore } from '../../stores/wallets'

const { t } = useI18n()
const wallets = useWalletsStore()
const { copyText } = useClipboard()
</script>

<template>
  <article class="card stack">
    <h2>{{ t('settings.catalog.title') }}</h2>
    <p class="muted">{{ t('settings.catalog.hint') }}</p>
    <div v-for="item in wallets.catalog" :key="item.asset" class="stack asset-info">
      <div class="row">
        <strong>{{ item.asset }}</strong>
        <span class="muted">{{ item.networkName }}</span>
      </div>
      <template v-if="item.kind === 'token'">
        <small>{{ item.standard }} · {{ t('settings.catalog.contractAddress') }}</small>
        <code class="address">{{ item.contractAddress }}</code>
        <div class="row">
          <button class="secondary" type="button" @click="copyText(item.contractAddress ?? '')">
            {{ t('settings.catalog.copyContract') }}
          </button>
          <a v-if="item.contractExplorerUrl" :href="item.contractExplorerUrl" target="_blank" rel="noopener noreferrer">
            {{ t('history.openExplorer') }}
          </a>
        </div>
      </template>
      <template v-else>
        <small class="muted">{{ t('settings.catalog.nativeNote') }}</small>
        <a :href="item.explorerUrl" target="_blank" rel="noopener noreferrer">{{ t('settings.catalog.networkExplorer') }}</a>
      </template>
    </div>
  </article>
</template>
