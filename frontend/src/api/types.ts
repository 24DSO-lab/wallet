export type WalletAsset = 'BTC' | 'ETH' | 'USDC' | 'USDT'
export type Asset = WalletAsset | 'TRX'
export type Profile = 'main' | 'test'

/** All monetary values are decimal strings in base units (satoshi, wei, ...). */
export interface Balance {
  asset: Asset
  network: string
  amount: string
  pendingAmount?: string
  decimals: number
  updatedAt: string
  errorCode?: string
  feeAsset?: string
  feeBalance?: string
  warnings?: string[]
}

export interface Wallet {
  id: string
  name: string
  balances: Balance[]
}

export interface AssetCatalogEntry {
  asset: Asset
  network: string
  networkName: string
  kind: 'native' | 'token'
  standard?: string
  contractAddress?: string
  explorerUrl: string
  contractExplorerUrl?: string
}

export interface PrepareWalletResult {
  mnemonic: string
  challengePositions: number[]
}

export interface Tx {
  id: string
  asset: WalletAsset
  direction: 'in' | 'out' | 'self'
  amount: string
  fee: string
  feeAsset?: Asset
  explorerUrl?: string
  status: 'pending' | 'confirmed' | 'failed' | 'replaced'
  canReplace?: boolean
  timestamp: string
  address: string
}

export interface Quote {
  draftId: string
  asset: WalletAsset
  network: string
  recipient: string
  amount: string
  fee: string
  feeAsset: string
  total: string
  warnings: string[]
  replacesTxId?: string
}

export interface SessionState {
  locked: boolean
  profile: Profile
  timeout: number
}

/** Per network: does the stored public data match the data re-derived from the seed? */
export interface AddressCheck {
  bitcoin: boolean
  ethereum: boolean
  tron: boolean
}

export interface ProviderSettings {
  etherscanAPIKeyConfigured: boolean
}

/** The contract between the Vue layer and Go (Wails facade or the UI mock). */
export interface Api {
  assetCatalog(): Promise<AssetCatalogEntry[]>
  session(): Promise<SessionState>
  providerSettings(): Promise<ProviderSettings>
  /** Path of the technical log file; empty when logging to a file is unavailable. */
  logPath(): Promise<string>
  saveEtherscanAPIKey(key: string): Promise<void>
  deleteEtherscanAPIKey(): Promise<void>
  keepAlive(): Promise<void>
  unlock(password: string): Promise<void>
  lock(): Promise<void>
  wallets(): Promise<Wallet[]>
  refresh(id: string, asset: Asset): Promise<Balance>
  create(name: string, seed: string, password: string, passphrase: string): Promise<Wallet>
  prepareWallet(name: string, words: 12 | 24): Promise<PrepareWalletResult>
  confirmCreateWallet(
    name: string,
    password: string,
    passphrase: string,
    positions: number[],
    answers: string[],
  ): Promise<Wallet>
  cancelCreateWallet(): Promise<void>
  importWallet(name: string, mnemonic: string, password: string, passphrase: string): Promise<Wallet>
  receive(id: string, asset: Asset): Promise<string>
  verifyAddresses(id: string, password: string, passphrase: string): Promise<AddressCheck>
  history(id: string, asset?: WalletAsset): Promise<Tx[]>
  prepare(id: string, asset: WalletAsset, to: string, amount: string): Promise<Quote>
  prepareRBF(walletId: string, txid: string): Promise<Quote>
  confirm(id: string, password: string, passphrase: string): Promise<void>
  profile(value: Profile): Promise<void>
  timeout(value: number): Promise<void>
  rename(id: string, name: string): Promise<void>
  backup(password: string): Promise<boolean>
  restoreBackup(password: string): Promise<boolean>
  changePassword(oldPassword: string, newPassword: string): Promise<void>
  revealSeed(id: string, password: string, passphrase: string): Promise<string>
  deleteWallet(id: string, confirmedName: string, password: string, passphrase: string): Promise<void>
}
