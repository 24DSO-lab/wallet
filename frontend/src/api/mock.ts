import type { Api, Asset, WalletAsset, Profile, Wallet, Tx, Quote, Balance } from './types'

const wait = (ms = 150) => new Promise<void>(resolve => setTimeout(resolve, ms))
const stamp = () => new Date().toISOString()
const decimals = (asset: Asset) => asset === 'BTC' ? 8 : asset === 'ETH' ? 18 : 6
const network = (asset: Asset) => asset === 'BTC' ? 'Bitcoin' : asset === 'ETH' || asset === 'USDC' ? 'Ethereum' : 'TRON'
const bal = (asset: Asset, amount: string, updatedAt = stamp()): Balance => ({
  asset, amount, decimals: decimals(asset), network: network(asset), updatedAt,
})

// Convert user-facing decimal strings to integer base units without floating point.
const toBaseUnits = (value: string, asset: Asset): string => {
  const [whole, fraction = ''] = value.split('.')
  const places = decimals(asset)
  if (fraction.length > places) throw Error('INVALID_SEND')
  return BigInt(`${whole}${fraction.padEnd(places, '0')}`).toString()
}

export function mockApi(): Api {
  let locked = true
  let net: Profile = 'test'
  let timeout = 5
  let items: Wallet[] = [{
    id: 'demo',
    name: 'Основной кошелёк',
    balances: [
      bal('BTC', '125000000'),
      bal('ETH', '2400000000000000000'),
      bal('USDC', '12550000'),
      bal('USDT', '85000000'),
      bal('TRX', '25000000'),
    ],
  }]
  let txs: Tx[] = []
  const drafts = new Map<string, Quote>()
  const guard = () => {
    if (locked) throw Error('SESSION_LOCKED')
  }

  return {
    async session() {
      await wait()
      return { locked, profile: net, timeout }
    },
    async keepAlive() { guard() },
    async unlock(password) {
      await wait()
      if (!password) throw Error('INVALID_PASSWORD')
      locked = false
    },
    async lock() { locked = true },
    async wallets() {
      await wait()
      guard()
      return structuredClone(items)
    },
    async refresh(id, asset) {
      await wait()
      guard()
      const wallet = items.find(item => item.id === id)
      if (!wallet) throw Error('WALLET_NOT_FOUND')
      const existing = wallet.balances.find(balance => balance.asset === asset)
      if (existing) return structuredClone(existing)
      const fresh = bal(asset, '0')
      wallet.balances = [...wallet.balances, fresh]
      return structuredClone(fresh)
    },
    async create(name) {
      await wait(300)
      guard()
      const wallet: Wallet = { id: crypto.randomUUID(), name, balances: [] }
      items = [...items, wallet]
      return structuredClone(wallet)
    },
    async prepareWallet(name, words) {
      await wait()
      guard()
      if (!name.trim() || (words !== 12 && words !== 24)) throw Error('INVALID_MNEMONIC_LENGTH')
      return {
        mnemonic: 'abandon ability able about above absent absorb abstract absurd abuse access accident',
        challengePositions: [0, 3, 11],
      }
    },
    async confirmCreateWallet(name, password, passphrase, positions, answers) {
      await wait(300)
      guard()
      void passphrase
      const phrase = 'abandon ability able about above absent absorb abstract absurd abuse access accident'.split(' ')
      if (password.length < 10 || positions.length !== 3 || answers.length !== 3 ||
        !positions.every((position, index) => [0, 3, 11][index] === position && phrase[position] === answers[index]?.trim().toLowerCase())) {
        throw Error('MNEMONIC_CHALLENGE_FAILED')
      }
      const wallet: Wallet = { id: crypto.randomUUID(), name, balances: [] }
      items = [...items, wallet]
      return structuredClone(wallet)
    },
    async cancelCreateWallet() { await wait() },
    async importWallet(name) {
      await wait(300)
      guard()
      const wallet: Wallet = { id: crypto.randomUUID(), name, balances: [] }
      items = [...items, wallet]
      return structuredClone(wallet)
    },
    async receive(_id, asset) {
      guard()
      await wait()
      if (asset === 'BTC') return 'tb1qtestaddress000000000000000000000000000'
      if (asset === 'USDT' || asset === 'TRX') return 'TQn9Y2khDD95J42FQtQTdwVVRx9h6f8z5D'
      return '0x742d35Cc6634C0532925a3b844Bc454e4438f44e'
    },
    async history(_id, asset) {
      guard()
      await wait()
      return txs.filter(tx => !asset || tx.asset === asset)
    },
    async prepare(_id, asset: WalletAsset, to, amount): Promise<Quote> {
      guard()
      await wait()
      if (!to.trim() || !/^\d+(\.\d+)?$/.test(amount) || !/[1-9]/.test(amount)) throw Error('INVALID_SEND')
      toBaseUnits(amount, asset) // Validate precision at prepare time, as the Go adapter does.
      const quote: Quote = {
        draftId: crypto.randomUUID(),
        asset,
        network: asset === 'BTC' ? 'Bitcoin testnet' : asset === 'USDT' ? 'TRON Nile' : 'Ethereum Sepolia',
        recipient: to,
        amount,
        fee: asset === 'BTC' ? '0.00001' : '0.000021',
        feeAsset: asset === 'USDC' ? 'ETH' : asset === 'USDT' ? 'TRX' : asset,
        total: amount,
        warnings: [],
      }
      drafts.set(quote.draftId, structuredClone(quote))
      return quote
    },
    async prepareRBF() {
      guard()
      throw Error('RBF_UNAVAILABLE_IN_MOCK')
    },
    async confirm(draftId, password) {
      guard()
      if (!password) throw Error('INVALID_PASSWORD')
      const quote = drafts.get(draftId)
      if (!quote) throw Error('DRAFT_NOT_FOUND')
      await wait(400)
      txs = [{
        id: crypto.randomUUID(),
        asset: quote.asset,
        direction: 'out',
        amount: toBaseUnits(quote.amount, quote.asset),
        fee: toBaseUnits(quote.fee, quote.feeAsset as Asset),
        status: 'pending',
        timestamp: stamp(),
        address: quote.recipient,
      }, ...txs]
      drafts.delete(draftId)
    },
    async profile(profile) { net = profile },
    async timeout(minutes) { timeout = minutes },
    async rename(id, name) {
      items = items.map(wallet => wallet.id === id ? { ...wallet, name } : wallet)
    },
    async backup(password) {
      guard()
      if (!password) throw Error('INVALID_PASSWORD')
      await wait()
      return true
    },
    async restoreBackup(password) {
      guard()
      if (!password) throw Error('INVALID_PASSWORD')
      await wait()
      return true
    },
    async changePassword(oldPassword, newPassword) {
      guard()
      if (!oldPassword || newPassword.length < 10) throw Error('INVALID_PASSWORD')
      await wait()
    },
    async revealSeed(id, password, passphrase) {
      guard()
      void passphrase
      if (!password || !items.some(wallet => wallet.id === id)) throw Error('INVALID_PASSWORD')
      await wait()
      return 'abandon ability able about above absent absorb abstract absurd abuse access accident'
    },
    async deleteWallet(id, confirmedName, password, passphrase) {
      guard()
      void passphrase
      const wallet = items.find(item => item.id === id)
      if (!wallet || wallet.name !== confirmedName || !password) throw Error('DELETE_CONFIRMATION_FAILED')
      items = items.filter(item => item.id !== id)
      await wait()
    },
  }
}
