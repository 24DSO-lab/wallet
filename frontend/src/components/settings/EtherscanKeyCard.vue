<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '../../api/client'
import { useUiStore } from '../../stores/ui'

const { t } = useI18n()
const ui = useUiStore()

const configured = ref(false)
const key = ref('')
const busy = ref(false)
const error = ref('')

async function load(): Promise<void> {
  error.value = ''
  try {
    configured.value = (await api.providerSettings()).etherscanAPIKeyConfigured
  } catch {
    error.value = t('settings.etherscan.loadFailed')
  }
}

async function save(): Promise<void> {
  busy.value = true
  error.value = ''
  try {
    await api.saveEtherscanAPIKey(key.value)
    configured.value = true
    ui.notice = t('settings.etherscan.saved')
  } catch {
    error.value = t('settings.etherscan.saveFailed')
  } finally {
    key.value = ''
    busy.value = false
  }
}

async function remove(): Promise<void> {
  busy.value = true
  error.value = ''
  try {
    await api.deleteEtherscanAPIKey()
    configured.value = false
    ui.notice = t('settings.etherscan.deleted')
  } catch {
    error.value = t('settings.etherscan.deleteFailed')
  } finally {
    busy.value = false
  }
}

onMounted(load)
onBeforeUnmount(() => {
  key.value = ''
})
</script>

<template>
  <article class="card stack">
    <h2>{{ t('settings.etherscan.title') }}</h2>
    <p class="muted">{{ t('settings.etherscan.hint') }}</p>
    <p>{{ configured ? t('settings.etherscan.configured') : t('settings.etherscan.notConfigured') }}</p>
    <label for="etherscan-api-key">API Key</label>
    <input
      id="etherscan-api-key"
      v-model="key"
      type="password"
      autocomplete="new-password"
      :placeholder="t('settings.etherscan.placeholder')">
    <div class="row">
      <button class="secondary" type="button" :disabled="busy || !key.trim()" @click="save">{{ t('settings.etherscan.save') }}</button>
      <button v-if="configured" class="quiet" type="button" :disabled="busy" @click="remove">{{ t('settings.etherscan.delete') }}</button>
    </div>
    <p v-if="error" class="error" role="alert">{{ error }}</p>
  </article>
</template>
