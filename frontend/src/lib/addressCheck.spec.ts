import { describe, expect, it } from 'vitest'
import { isAddressVerified } from './addressCheck'

describe('isAddressVerified', () => {
  const check = { bitcoin: true, ethereum: false, tron: true }

  it('maps each asset to the network that provides its address', () => {
    expect(isAddressVerified(check, 'BTC')).toBe(true)
    expect(isAddressVerified(check, 'ETH')).toBe(false)
    expect(isAddressVerified(check, 'USDC')).toBe(false)
    expect(isAddressVerified(check, 'TRX')).toBe(true)
    expect(isAddressVerified(check, 'USDT')).toBe(true)
  })

  it('fails closed when every network mismatches', () => {
    const none = { bitcoin: false, ethereum: false, tron: false }
    for (const asset of ['BTC', 'ETH', 'USDC', 'TRX', 'USDT'] as const) {
      expect(isAddressVerified(none, asset)).toBe(false)
    }
  })
})
