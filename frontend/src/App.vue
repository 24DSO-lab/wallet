<script setup lang="ts">
import { onBeforeUnmount, onMounted, watch } from 'vue'
import { RouterView, useRoute, useRouter } from 'vue-router'
import AppShell from './components/AppShell.vue'
import { useSessionStore } from './stores/session'
import { useWalletsStore } from './stores/wallets'

const REFRESH_INTERVAL_MS = 300_000
const SESSION_POLL_MS = 15_000
const ACTIVITY_EVENTS = ['pointerdown', 'keydown', 'wheel', 'touchstart'] as const

const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const wallets = useWalletsStore()

let refreshTimer: ReturnType<typeof globalThis.setInterval> | undefined
let pollTimer: ReturnType<typeof globalThis.setInterval> | undefined

// Go can lock the session by itself (idle timeout); follow it to the lock screen.
watch(
  () => session.locked,
  locked => {
    if (locked && route.name !== 'lock') void router.replace({ name: 'lock' })
  },
)

onMounted(() => {
  for (const event of ACTIVITY_EVENTS) {
    globalThis.document.addEventListener(event, session.userActivity, { passive: true })
  }
  if (!session.locked) void wallets.refreshAll()
  refreshTimer = globalThis.setInterval(() => {
    if (!session.locked) void wallets.refreshAll()
  }, REFRESH_INTERVAL_MS)
  pollTimer = globalThis.setInterval(() => void session.poll(), SESSION_POLL_MS)
})

onBeforeUnmount(() => {
  for (const event of ACTIVITY_EVENTS) globalThis.document.removeEventListener(event, session.userActivity)
  if (refreshTimer) globalThis.clearInterval(refreshTimer)
  if (pollTimer) globalThis.clearInterval(pollTimer)
})
</script>

<template>
  <RouterView v-if="route.meta.bare" />
  <AppShell v-else>
    <RouterView />
  </AppShell>
</template>
