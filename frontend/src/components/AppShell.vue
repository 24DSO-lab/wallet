<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute } from 'vue-router'
import { useSessionStore } from '../stores/session'
import { useUiStore } from '../stores/ui'

const { t } = useI18n()
const route = useRoute()
const session = useSessionStore()
const ui = useUiStore()

const title = computed(() => t(route.meta.title ?? 'title.wallet'))
const isTest = computed(() => session.profile === 'test')
</script>

<template>
  <div class="layout">
    <aside>
      <div class="brand"><b>W</b> {{ t('app.name') }}</div>
      <span class="badge" :class="{ main: !isTest }">{{ isTest ? t('network.testBadge') : t('network.mainBadge') }}</span>
      <nav :aria-label="t('nav.label')">
        <RouterLink :to="{ name: 'home' }">⌂ {{ t('nav.home') }}</RouterLink>
        <RouterLink :to="{ name: 'wizard' }">＋ {{ t('nav.addWallet') }}</RouterLink>
        <RouterLink :to="{ name: 'history' }">↔ {{ t('nav.history') }}</RouterLink>
        <RouterLink :to="{ name: 'settings' }">⚙ {{ t('nav.settings') }}</RouterLink>
      </nav>
      <button class="quiet" type="button" @click="session.lock()">{{ t('nav.lock') }} ↪</button>
    </aside>
    <main class="content">
      <header>
        <div>
          <small>NON-CUSTODIAL DESKTOP WALLET</small>
          <h1>{{ title }}</h1>
        </div>
        <span class="network">{{ isTest ? t('network.testList') : t('network.mainList') }}</span>
      </header>
      <p v-if="ui.error" class="error" role="alert">{{ ui.error }}</p>
      <p v-if="ui.notice" class="muted" role="status">{{ ui.notice }}</p>
      <slot />
    </main>
  </div>
</template>
