import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api } from '../api/client'
import type { Asset, AssetCatalogEntry, Wallet, WalletAsset } from '../api/types'
import { i18n } from '../i18n'
import { errorCode } from '../lib/errors'
import { useUiStore } from './ui'

export const SENDABLE_ASSETS: readonly WalletAsset[] = ['BTC', 'ETH', 'USDC', 'USDT']
export const REFRESH_ASSETS: readonly Asset[] = [...SENDABLE_ASSETS, 'TRX']

type RefreshResult = 'ok' | 'warning' | 'failed'

const assetKey = (walletId: string, asset: Asset): string => `${walletId}:${asset}`

export const useWalletsStore = defineStore('wallets', () => {
  const ui = useUiStore()
  const t = i18n.global.t

  const wallets = ref<Wallet[]>([])
  const selectedId = ref('')
  const catalog = ref<AssetCatalogEntry[]>([])
  const warnings = ref<Record<string, Record<string, string[]>>>({})
  const refreshingAssets = ref<Record<string, boolean>>({})
  const refreshingId = ref('')

  const current = computed(() => wallets.value.find(wallet => wallet.id === selectedId.value))

  function assetMeta(asset: string): AssetCatalogEntry | undefined {
    return catalog.value.find(entry => entry.asset === asset)
  }

  function isAssetRefreshing(walletId: string, asset: Asset): boolean {
    return Boolean(refreshingAssets.value[assetKey(walletId, asset)])
  }

  function warningsFor(walletId: string, asset: string): string[] {
    return warnings.value[walletId]?.[asset] ?? []
  }

  async function loadCatalog(): Promise<void> {
    try {
      catalog.value = await api.assetCatalog()
    } catch {
      catalog.value = []
    }
  }

  async function load(): Promise<void> {
    wallets.value = await api.wallets()
    if (!current.value) selectedId.value = wallets.value[0]?.id ?? ''
  }

  function select(id: string): void {
    selectedId.value = id
  }

  function add(wallet: Wallet): void {
    wallets.value = [...wallets.value, wallet]
    selectedId.value = wallet.id
  }

  function remove(id: string): void {
    wallets.value = wallets.value.filter(wallet => wallet.id !== id)
    if (selectedId.value === id) selectedId.value = wallets.value[0]?.id ?? ''
  }

  function setWarnings(walletId: string, asset: Asset, list: string[]): void {
    warnings.value = {
      ...warnings.value,
      [walletId]: { ...(warnings.value[walletId] ?? {}), [asset]: list },
    }
  }

  async function refreshAsset(walletId: string, asset: Asset, reload = true): Promise<RefreshResult> {
    const key = assetKey(walletId, asset)
    if (refreshingAssets.value[key]) return 'ok'
    refreshingAssets.value = { ...refreshingAssets.value, [key]: true }
    try {
      const result = await api.refresh(walletId, asset)
      const list = result.warnings ?? []
      setWarnings(walletId, asset, list)
      if (reload) wallets.value = await api.wallets()
      return list.length ? 'warning' : 'ok'
    } catch (e) {
      // The stable Go code tells the user (and us) why the provider call failed.
      setWarnings(walletId, asset, [`refresh_failed:${errorCode(e)}`])
      return 'failed'
    } finally {
      const next = { ...refreshingAssets.value }
      delete next[key]
      refreshingAssets.value = next
    }
  }

  async function refreshWallet(walletId: string, select = true): Promise<void> {
    if (refreshingId.value === walletId) return
    refreshingId.value = walletId
    if (select) selectedId.value = walletId
    ui.error = ''
    try {
      const results = await Promise.all(REFRESH_ASSETS.map(asset => refreshAsset(walletId, asset, false)))
      const failed = REFRESH_ASSETS.filter((_, index) => results[index] === 'failed')
      const messages: string[] = []
      if (failed.length) messages.push(t('refresh.failedAssets', { assets: failed.join(', ') }))
      if (results.includes('warning')) messages.push(t('refresh.partial'))
      try {
        wallets.value = await api.wallets()
      } catch {
        messages.push(t('refresh.reloadFailed'))
      }
      if (messages.length) ui.error = messages.join(' ')
    } catch {
      ui.error = t('refresh.failed')
    } finally {
      refreshingId.value = ''
    }
  }

  async function refreshAll(): Promise<void> {
    for (const wallet of wallets.value) await refreshWallet(wallet.id, false)
  }

  /** Drops every wallet-derived value; called whenever the session locks. */
  function reset(): void {
    wallets.value = []
    selectedId.value = ''
    warnings.value = {}
    refreshingAssets.value = {}
    refreshingId.value = ''
  }

  return {
    wallets, selectedId, catalog, refreshingId, current,
    assetMeta, isAssetRefreshing, warningsFor,
    loadCatalog, load, select, add, remove,
    refreshAsset, refreshWallet, refreshAll, reset,
  }
})
