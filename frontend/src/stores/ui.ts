import { defineStore } from 'pinia'
import { ref, watch } from 'vue'

/** Informational notices disappear on their own; errors stay until dismissed or navigation. */
export const NOTICE_TIMEOUT_MS = 6000

/** Page-level feedback shown by the application shell. Never holds secrets. */
export const useUiStore = defineStore('ui', () => {
  const error = ref('')
  const notice = ref('')
  let timer: ReturnType<typeof globalThis.setTimeout> | undefined

  watch(notice, value => {
    if (timer) globalThis.clearTimeout(timer)
    timer = undefined
    if (!value) return
    timer = globalThis.setTimeout(() => {
      notice.value = ''
      timer = undefined
    }, NOTICE_TIMEOUT_MS)
  })

  function clear(): void {
    error.value = ''
    notice.value = ''
  }

  return { error, notice, clear }
})
