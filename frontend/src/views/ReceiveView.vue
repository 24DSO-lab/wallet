<script setup lang="ts">
import QRCode from 'qrcode'
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { api } from '../api/client'
import type { Asset } from '../api/types'
import VerifyAddress from '../components/VerifyAddress.vue'
import { useClipboard } from '../lib/clipboard'
import { useWalletsStore } from '../stores/wallets'

const props = defineProps<{ id: string; asset: string }>()

const { t } = useI18n()
const router = useRouter()
const wallets = useWalletsStore()
const { copyText } = useClipboard()

const address = ref('')
const qr = ref('')
const busy = ref(false)
const error = ref('')
// Set when the seed-derived check disagrees with the stored data: the address must not be offered.
const unsafe = ref(false)
const typedAsset = computed(() => props.asset as Asset)
const networkName = computed(() => wallets.assetMeta(props.asset)?.networkName ?? '')

function onVerified(verified: boolean): void {
  unsafe.value = !verified
}

async function load(): Promise<void> {
  if (busy.value) return
  address.value = ''
  qr.value = ''
  error.value = ''
  busy.value = true
  try {
    const next = await api.receive(props.id, typedAsset.value)
    if (!next.trim()) throw new Error('empty_receive_address')
    address.value = next
    qr.value = await QRCode.toDataURL(next, { width: 220, margin: 1 })
  } catch (e) {
    globalThis.console.error('Receive address request failed:', props.asset, e instanceof Error ? e.message : 'unknown error')
    error.value = t('receive.failed')
  } finally {
    busy.value = false
  }
}

onMounted(() => {
  wallets.select(props.id)
  void load()
})
</script>

<template>
  <section class="card narrow stack">
    <h2>{{ t('wallet.receive', { asset }) }}</h2>
    <p class="warning">{{ t('receive.warning', { asset }) }}</p>
    <span v-if="networkName" class="network">{{ networkName }}</span>
    <p v-if="busy" class="muted" role="status">{{ asset === 'BTC' ? t('receive.searchingBtc') : t('receive.loading') }}</p>
    <p v-if="error" class="error" role="alert">{{ error }}</p>
    <p v-if="unsafe" class="error" role="alert">{{ t('receive.unsafe') }}</p>
    <img v-if="qr && !unsafe" :src="qr" class="qr" :alt="t('receive.qrAlt')">
    <code v-if="address && !unsafe" class="seed">{{ address }}</code>
    <button v-if="address && !unsafe" class="primary" type="button" @click="copyText(address)">
      {{ t('receive.copyAddress') }}
    </button>
    <VerifyAddress v-if="address" :wallet-id="id" :asset="typedAsset" @result="onVerified" />
    <button v-if="error" class="primary" type="button" :disabled="busy" @click="load">{{ t('common.retry') }}</button>
    <button class="quiet" type="button" :disabled="busy" @click="router.push({ name: 'wallet', params: { id } })">
      {{ t('common.back') }}
    </button>
  </section>
</template>
