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
        <RouterLink :to="{ name: 'home' }">
          <svg class="ico" viewBox="0 0 24 24" aria-hidden="true"><path d="M3 11l9-8 9 8v9a1 1 0 0 1-1 1h-5v-6H9v6H4a1 1 0 0 1-1-1z" /></svg>
          {{ t('nav.home') }}
        </RouterLink>
        <RouterLink :to="{ name: 'history' }">
          <svg class="ico" viewBox="0 0 24 24" aria-hidden="true"><path d="M4 8h14m-4-4 4 4-4 4M20 16H6m4 4-4-4 4-4" /></svg>
          {{ t('nav.history') }}
        </RouterLink>
        <RouterLink :to="{ name: 'settings' }">
          <svg class="ico" viewBox="0 0 24 24" aria-hidden="true">
            <path d="M4 6h8M18 6h2M4 12h2M12 12h8M4 18h10M20 18h0" />
            <circle cx="15" cy="6" r="2" /><circle cx="9" cy="12" r="2" /><circle cx="17" cy="18" r="2" />
          </svg>
          {{ t('nav.settings') }}
        </RouterLink>
      </nav>
      <button class="quiet" type="button" @click="session.lock()">
        <svg class="ico" viewBox="0 0 24 24" aria-hidden="true"><rect x="5" y="11" width="14" height="9" rx="2" /><path d="M8 11V8a4 4 0 0 1 8 0v3" /></svg>
        {{ t('nav.lock') }}
      </button>
    </aside>
    <main class="content">
      <header>
        <h1>{{ title }}</h1>
        <span class="network">{{ isTest ? t('network.testList') : t('network.mainList') }}</span>
      </header>
      <p v-if="ui.error" class="error" role="alert">{{ ui.error }}</p>
      <p v-if="ui.notice" class="muted" role="status">{{ ui.notice }}</p>
      <slot />
    </main>
  </div>
</template>
