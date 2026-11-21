<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '../api/client'
import type { Asset } from '../api/types'
import { isAddressVerified } from '../lib/addressCheck'

const props = defineProps<{ walletId: string; asset: Asset }>()
const emit = defineEmits<{ result: [verified: boolean] }>()

const { t } = useI18n()
const open = ref(false)
const password = ref('')
const passphrase = ref('')
const busy = ref(false)
const verified = ref<boolean | undefined>()
const error = ref('')

function clearSecrets(): void {
  password.value = ''
  passphrase.value = ''
}

async function verify(): Promise<void> {
  if (!password.value) return
  busy.value = true
  error.value = ''
  verified.value = undefined
  const secret = { password: password.value, passphrase: passphrase.value }
  clearSecrets()
  try {
    const check = await api.verifyAddresses(props.walletId, secret.password, secret.passphrase)
    verified.value = isAddressVerified(check, props.asset)
    emit('result', verified.value)
  } catch {
    error.value = t('receive.verify.failed')
  } finally {
    busy.value = false
  }
}

onBeforeUnmount(clearSecrets)
</script>

<template>
  <div class="stack">
    <button v-if="!open" class="secondary" type="button" @click="open = true">{{ t('receive.verify.open') }}</button>
    <form v-else class="stack" @submit.prevent="verify">
      <p class="muted">{{ t('receive.verify.hint') }}</p>
      <label for="verify-password">{{ t('common.password') }}</label>
      <input id="verify-password" v-model="password" type="password" autocomplete="off">
      <label for="verify-passphrase">{{ t('common.passphraseIfSet') }}</label>
      <input id="verify-passphrase" v-model="passphrase" type="password" autocomplete="off">
      <button class="secondary" type="submit" :disabled="busy || !password">
        {{ busy ? t('receive.verify.running') : t('receive.verify.submit') }}
      </button>
    </form>
    <p v-if="verified === true" class="success" role="status">{{ t('receive.verify.ok') }}</p>
    <p v-if="verified === false" class="error" role="alert">{{ t('receive.verify.mismatch') }}</p>
    <p v-if="error" class="error" role="alert">{{ error }}</p>
  </div>
</template>
