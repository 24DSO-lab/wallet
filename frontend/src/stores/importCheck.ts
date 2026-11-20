import { defineStore } from 'pinia'
import { ref } from 'vue'
import type { Asset } from '../api/types'

export interface ImportedAddress {
  asset: Asset
  address: string
}

/** Public addresses shown after an import so the user can compare them with the source wallet. */
export const useImportCheckStore = defineStore('importCheck', () => {
  const addresses = ref<ImportedAddress[]>([])

  function clear(): void {
    addresses.value = []
  }

  return { addresses, clear }
})
