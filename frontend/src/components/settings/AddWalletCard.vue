<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

const { t } = useI18n()
const router = useRouter()
</script>

<template>
  <article class="card stack">
    <h2>{{ t('settings.addWallet.title') }}</h2>
    <p class="muted">{{ t('settings.addWallet.hint') }}</p>
    <div>
      <button class="primary" type="button" @click="router.push({ name: 'wizard' })">
        {{ t('settings.addWallet.action') }}
      </button>
    </div>
  </article>
</template>
