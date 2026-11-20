<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useSessionStore } from '../../stores/session'

const MINUTES = [1, 5, 10, 15, 30] as const

const { t } = useI18n()
const session = useSessionStore()
const minutes = ref(session.timeout)

watch(
  () => session.timeout,
  value => {
    minutes.value = value
  },
)

watch(minutes, value => {
  if (value !== session.timeout) void session.setLockTimeout(value)
})
</script>

<template>
  <article class="card stack">
    <h2>{{ t('settings.autolock.title') }}</h2>
    <select v-model.number="minutes" :aria-label="t('settings.autolock.title')">
      <option v-for="value in MINUTES" :key="value" :value="value">{{ t('settings.autolock.minutes', value) }}</option>
    </select>
  </article>
</template>
