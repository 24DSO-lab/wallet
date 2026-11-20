<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useImportCheckStore } from '../stores/importCheck'

const { t } = useI18n()
const router = useRouter()
const check = useImportCheckStore()

function done(): void {
  check.clear()
  void router.push({ name: 'home' })
}
</script>

<template>
  <section class="stack narrow">
    <article class="card stack">
      <h2>{{ t('importCheck.title') }}</h2>
      <p class="muted">{{ t('importCheck.hint') }}</p>
      <div v-for="entry in check.addresses" :key="entry.asset" class="stack">
        <strong>{{ entry.asset }}</strong>
        <code class="address">{{ entry.address }}</code>
      </div>
      <p v-if="!check.addresses.length" class="error">{{ t('importCheck.none') }}</p>
      <p class="warning">{{ t('importCheck.mismatch') }}</p>
      <button class="primary" type="button" @click="done">{{ t('importCheck.confirm') }}</button>
    </article>
  </section>
</template>
