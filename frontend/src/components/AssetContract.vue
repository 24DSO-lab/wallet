<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useClipboard } from '../lib/clipboard'
import { useWalletsStore } from '../stores/wallets'

const props = defineProps<{ asset: string }>()

const { t } = useI18n()
const wallets = useWalletsStore()
const { copyText } = useClipboard()
const meta = computed(() => wallets.assetMeta(props.asset))
</script>

<template>
  <small v-if="meta?.kind === 'token'" class="muted">{{ meta.standard }} · {{ t('asset.contract') }}</small>
  <div v-if="meta?.contractAddress" class="contract-row">
    <code>{{ meta.contractAddress }}</code>
    <button class="quiet" type="button" @click="copyText(meta.contractAddress ?? '')">{{ t('common.copy') }}</button>
    <a v-if="meta.contractExplorerUrl" :href="meta.contractExplorerUrl" target="_blank" rel="noopener noreferrer">
      {{ t('asset.explorer') }}
    </a>
  </div>
  <small v-else class="muted">{{ t('asset.native') }}</small>
</template>
