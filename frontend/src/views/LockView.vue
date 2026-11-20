<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useSessionStore } from '../stores/session'

const { t } = useI18n()
const router = useRouter()
const session = useSessionStore()

const password = ref('')
const error = ref('')
const busy = ref(false)
const isTest = computed(() => session.profile === 'test')

async function submit(): Promise<void> {
  busy.value = true
  error.value = ''
  const unlocked = await session.unlock(password.value)
  password.value = ''
  busy.value = false
  if (unlocked) await router.replace({ name: 'home' })
  else error.value = t('lock.failed')
}

onBeforeUnmount(() => {
  password.value = ''
})
</script>

<template>
  <main class="lock">
    <section class="card lock-card">
      <div class="logo">W</div>
      <p class="muted">{{ t('lock.tagline') }}</p>
      <h1>{{ t('lock.title') }}</h1>
      <form @submit.prevent="submit">
        <label for="wallet-unlock-password">{{ t('common.password') }}</label>
        <input id="wallet-unlock-password" v-model="password" type="password" autocomplete="off" autofocus required>
        <p v-if="error" class="error" role="alert">{{ error }}</p>
        <button type="submit" class="primary" :disabled="busy || session.initializing">{{ t('lock.unlock') }}</button>
      </form>
      <span class="badge" :class="{ main: !isTest }">{{ isTest ? t('network.testBadge') : t('network.mainBadgeLong') }}</span>
    </section>
  </main>
</template>
