import { defineStore } from 'pinia'
import { ref } from 'vue'

/** Page-level feedback shown by the application shell. Never holds secrets. */
export const useUiStore = defineStore('ui', () => {
  const error = ref('')
  const notice = ref('')

  function clear(): void {
    error.value = ''
    notice.value = ''
  }

  return { error, notice, clear }
})
