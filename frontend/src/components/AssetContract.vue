<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useClipboard } from '../lib/clipboard'
import { shortId } from '../lib/text'
import { useWalletsStore } from '../stores/wallets'

const props = defineProps<{ asset: string }>()

const { t } = useI18n()
const wallets = useWalletsStore()
const { copyText } = useClipboard()
const meta = computed(() => wallets.assetMeta(props.asset))
</script>

<template>
  <!-- Only tokens have a contract. It is the same for every wallet, so it stays compact. -->
  <div v-if="meta?.contractAddress" class="contract">
    <span>{{ meta.standard }} · {{ t('asset.contract') }}</span>
    <code :title="meta.contractAddress">{{ shortId(meta.contractAddress) }}</code>
    <button class="quiet" type="button" @click="copyText(meta.contractAddress ?? '')">{{ t('common.copy') }}</button>
    <a v-if="meta.contractExplorerUrl" :href="meta.contractExplorerUrl" target="_blank" rel="noopener noreferrer">
      {{ t('asset.explorer') }}
    </a>
  </div>
</template>
