import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '../api/client'
import type { Profile } from '../api/types'
import { useSendStore } from './send'
import { useUiStore } from './ui'
import { useWalletsStore } from './wallets'

const KEEP_ALIVE_THROTTLE_MS = 5000

export const useSessionStore = defineStore('session', () => {
  const ui = useUiStore()
  const wallets = useWalletsStore()
  const send = useSendStore()

  const initializing = ref(true)
  const locked = ref(true)
  const profile = ref<Profile>('test')
  const timeout = ref(5)
  let lastActivityAt = 0

  function clearData(): void {
    wallets.reset()
    send.reset()
    ui.clear()
  }

  /** Reads session state and, when unlocked, the wallet list. */
  async function init(): Promise<void> {
    try {
      const state = await api.session()
      locked.value = state.locked
      profile.value = state.profile
      timeout.value = state.timeout
      await wallets.loadCatalog()
      if (state.locked) clearData()
      else await wallets.load()
    } catch (e) {
      ui.error = String(e)
    } finally {
      initializing.value = false
    }
  }

  /** Returns true only when Go confirms the session is really unlocked. */
  async function unlock(password: string): Promise<boolean> {
    try {
      await api.unlock(password)
      const state = await api.session()
      if (state.locked) throw new Error('UNLOCK_NOT_CONFIRMED')
      const list = await api.wallets()
      locked.value = false
      profile.value = state.profile
      timeout.value = state.timeout
      wallets.wallets = list
      if (!wallets.current) wallets.select(list[0]?.id ?? '')
      void wallets.refreshAll()
      return true
    } catch (e) {
      globalThis.console.error('Wallet unlock failed:', e instanceof Error ? e.message : 'unknown error')
      locked.value = true
      clearData()
      return false
    }
  }

  async function lock(): Promise<void> {
    await api.lock()
    locked.value = true
    clearData()
  }

  /** Best-effort polling: Go may lock the session on its own timeout. */
  async function poll(): Promise<void> {
    try {
      const state = await api.session()
      if (profile.value !== state.profile) {
        profile.value = state.profile
        await wallets.loadCatalog()
      }
      timeout.value = state.timeout
      if (state.locked && !locked.value) {
        locked.value = true
        clearData()
      }
    } catch {
      // The next tick retries.
    }
  }

  function userActivity(): void {
    if (locked.value) return
    const now = Date.now()
    if (now - lastActivityAt < KEEP_ALIVE_THROTTLE_MS) return
    lastActivityAt = now
    void api.keepAlive().catch(() => undefined)
  }

  async function setProfile(next: Profile): Promise<void> {
    await api.profile(next)
    profile.value = next
    await init()
  }

  async function setLockTimeout(minutes: number): Promise<void> {
    await api.timeout(minutes)
    timeout.value = minutes
  }

  return { initializing, locked, profile, timeout, init, unlock, lock, poll, userActivity, setProfile, setLockTimeout }
})
