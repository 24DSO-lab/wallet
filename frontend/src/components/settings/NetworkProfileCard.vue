<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useSessionStore } from '../../stores/session'

const { t } = useI18n()
const session = useSessionStore()
</script>

<template>
  <article class="card stack">
    <h2>{{ t('settings.profile.title') }}</h2>
    <div class="row">
      <button type="button" :class="{ selected: session.profile === 'test' }" @click="session.setProfile('test')">
        {{ t('settings.profile.test') }}
      </button>
      <button type="button" :class="{ selected: session.profile === 'main' }" disabled :title="t('settings.profile.mainHint')">
        {{ t('settings.profile.main') }}
      </button>
    </div>
    <p v-if="session.profile === 'test'" class="warning">{{ t('network.testList') }}</p>
  </article>
</template>
