import type { Asset, AddressCheck } from '../api/types'

type CheckedNetwork = keyof AddressCheck

/** USDC shares the Ethereum account address, USDT and TRX share the TRON one. */
const NETWORK_OF_ASSET: Readonly<Record<Asset, CheckedNetwork>> = {
  BTC: 'bitcoin',
  ETH: 'ethereum',
  USDC: 'ethereum',
  TRX: 'tron',
  USDT: 'tron',
}

/** Whether the check covers `asset`'s receive data and found it genuine. */
export function isAddressVerified(check: AddressCheck, asset: Asset): boolean {
  return check[NETWORK_OF_ASSET[asset]]
}
