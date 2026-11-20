<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useSendStore } from '../stores/send'
import { useUiStore } from '../stores/ui'

const { t } = useI18n()
const router = useRouter()
const send = useSendStore()
const ui = useUiStore()

const password = ref('')
const passphrase = ref('')
const busy = ref(false)

function clearSecrets(): void {
  password.value = ''
  passphrase.value = ''
}

async function submit(): Promise<void> {
  busy.value = true
  const secret = { password: password.value, passphrase: passphrase.value }
  clearSecrets()
  try {
    await send.confirm(secret.password, secret.passphrase)
    await router.push({ name: 'history' })
  } catch (e) {
    const message = String(e)
    const unknown = message.includes('broadcast_unknown')
    const duplicate = message.includes('duplicate_send')
    if (unknown || duplicate) {
      send.reset()
      await router.push({ name: 'history' })
      // The router guard clears feedback on navigation, so set it afterwards.
      ui.error = unknown ? t('send.broadcastUnknown') : t('send.duplicateBlocked')
    } else {
      ui.error = t('send.notConfirmed')
    }
  } finally {
    busy.value = false
  }
}

onBeforeUnmount(clearSecrets)
</script>

<template>
  <section class="card narrow stack">
    <h2>{{ t('confirm.title') }}</h2>
    <form class="stack" @submit.prevent="submit">
      <label for="confirm-password">{{ t('common.password') }}</label>
      <input id="confirm-password" v-model="password" type="password" autocomplete="off">
      <label for="confirm-passphrase">{{ t('common.passphrase') }}</label>
      <input id="confirm-passphrase" v-model="passphrase" type="password" autocomplete="off">
      <p class="warning">{{ t('confirm.note') }}</p>
      <button class="primary" type="submit" :disabled="busy">{{ t('confirm.submit') }}</button>
    </form>
  </section>
</template>
