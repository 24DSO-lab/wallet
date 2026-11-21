import type {
  AddressCheck,
  Api,
  AssetCatalogEntry,
  Balance,
  PrepareWalletResult,
  ProviderSettings,
  Quote,
  SessionState,
  Tx,
  Wallet,
} from './types'

type Facade = Record<string, (...args: unknown[]) => Promise<unknown>>

declare global {
  interface Window {
    go?: { app?: { App?: Facade } }
  }
}

// Async on purpose: a missing binding must surface as a rejected promise so
// callers' `.catch()` chains and try/catch blocks behave the same way.
async function invoke<T>(method: string, ...args: unknown[]): Promise<T> {
  const fn = window.go?.app?.App?.[method]
  if (!fn) throw new Error(`WAILS_METHOD_UNAVAILABLE:${method}`)
  return (await fn(...args)) as T
}

/**
 * Binds the Api contract to the Go facade in app/app.go. DTOs mirror
 * app/dto.go; amounts and balances stay decimal strings end to end.
 */
export function wailsApi(): Api {
  return {
    assetCatalog: () => invoke<AssetCatalogEntry[]>('GetAssetCatalog'),
    session: () => invoke<SessionState>('GetSession'),
    providerSettings: () => invoke<ProviderSettings>('GetProviderSettings'),
    logPath: () => invoke<string>('GetLogPath'),
    saveEtherscanAPIKey: key => invoke<void>('SaveEtherscanAPIKey', key),
    deleteEtherscanAPIKey: () => invoke<void>('DeleteEtherscanAPIKey'),
    keepAlive: () => invoke<void>('KeepAlive'),
    unlock: password => invoke<void>('Unlock', password),
    lock: () => invoke<void>('Lock'),
    wallets: () => invoke<Wallet[]>('ListWallets'),
    refresh: (id, asset) => invoke<Balance>('RefreshWallet', id, asset),
    // Creation always goes through the verified two-step flow below.
    create: () => Promise.reject(new Error('USE_CREATE_WALLET_OR_IMPORT_WALLET')),
    prepareWallet: (name, words) => invoke<PrepareWalletResult>('PrepareWallet', name, words),
    confirmCreateWallet: (name, password, passphrase, positions, answers) =>
      invoke<Wallet>('ConfirmCreateWallet', name, password, passphrase, positions, answers),
    cancelCreateWallet: () => invoke<void>('CancelCreateWallet'),
    importWallet: (name, mnemonic, password, passphrase) =>
      invoke<Wallet>('ImportWallet', name, mnemonic, password, passphrase),
    receive: (id, asset) => invoke<string>('GetReceiveAddress', id, asset),
    verifyAddresses: (id, password, passphrase) =>
      invoke<AddressCheck>('VerifyAddresses', id, password, passphrase),
    history: (id, asset) => invoke<Tx[]>('GetHistory', id, asset ?? ''),
    prepare: (id, asset, to, amount) => invoke<Quote>('PrepareSend', id, asset, to, amount),
    prepareRBF: (walletId, txid) => invoke<Quote>('PrepareRBF', walletId, txid),
    confirm: (id, password, passphrase) => invoke<void>('ConfirmSend', id, password, passphrase),
    profile: value => invoke<void>('SetProfile', value),
    timeout: value => invoke<void>('SetLockTimeout', value),
    rename: (id, name) => invoke<void>('RenameWallet', id, name),
    backup: password => invoke<boolean>('CreateBackup', password),
    restoreBackup: password => invoke<boolean>('RestoreBackup', password),
    changePassword: (oldPassword, newPassword) => invoke<void>('ChangePassword', oldPassword, newPassword),
    revealSeed: (id, password, passphrase) => invoke<string>('RevealSeed', id, password, passphrase),
    deleteWallet: (id, confirmedName, password, passphrase) =>
      invoke<void>('DeleteWallet', id, confirmedName, password, passphrase),
  }
}
