import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '../api/client'
import type { Quote, WalletAsset } from '../api/types'

/**
 * Holds the non-secret part of a send flow. Passwords and BIP39 passphrases
 * stay in the component that collects them and are never stored here.
 */
export const useSendStore = defineStore('send', () => {
  const asset = ref<WalletAsset>('BTC')
  const recipient = ref('')
  const amount = ref('')
  const quote = ref<Quote>()

  async function prepare(walletId: string): Promise<void> {
    quote.value = await api.prepare(walletId, asset.value, recipient.value, amount.value)
  }

  async function prepareRBF(walletId: string, txid: string): Promise<void> {
    quote.value = await api.prepareRBF(walletId, txid)
  }

  async function confirm(password: string, passphrase: string): Promise<void> {
    if (!quote.value) return
    await api.confirm(quote.value.draftId, password, passphrase)
    reset()
  }

  function reset(): void {
    asset.value = 'BTC'
    recipient.value = ''
    amount.value = ''
    quote.value = undefined
  }

  return { asset, recipient, amount, quote, prepare, prepareRBF, confirm, reset }
})
