import { describe, expect, it } from 'vitest'
import { mockApi } from './mock'

describe('mock wallet creation lifecycle', () => {
  it('does not persist a wallet until the recovery challenge is verified', async () => {
    const api = mockApi()
    await api.unlock('test-password')

    const before = await api.wallets()
    const pending = await api.prepareWallet('New wallet', 12)
    expect(pending.challengePositions).toEqual([0, 3, 11])

    await expect(api.confirmCreateWallet(
      'New wallet',
      'long-test-password',
      '',
      pending.challengePositions,
      ['wrong', 'about', 'accident'],
    )).rejects.toThrow('MNEMONIC_CHALLENGE_FAILED')

    expect(await api.wallets()).toHaveLength(before.length)

    await api.confirmCreateWallet(
      'New wallet',
      'long-test-password',
      '',
      pending.challengePositions,
      ['abandon', 'about', 'accident'],
    )

    expect(await api.wallets()).toHaveLength(before.length + 1)
  })

  it('rejects wallet creation after the session is locked', async () => {
    const api = mockApi()
    await api.unlock('test-password')
    await api.lock()

    await expect(api.prepareWallet('New wallet', 12)).rejects.toThrow('SESSION_LOCKED')
  })
})


describe('mock transfer lifecycle', () => {
  it('records the exact asset, amount, fee and recipient from the confirmed quote', async () => {
    const api = mockApi()
    await api.unlock('test-password')

    const quote = await api.prepare('demo', 'BTC', 'tb1qactualrecipient', '0.001')
    await api.confirm(quote.draftId, 'test-password', '')

    const history = await api.history('demo')
    expect(history).toHaveLength(1)
    expect(history[0]).toEqual(expect.objectContaining({
      asset: 'BTC',
      direction: 'out',
      amount: '100000',
      fee: '1000',
      status: 'pending',
      address: 'tb1qactualrecipient',
    }))
    await expect(api.confirm(quote.draftId, 'test-password', '')).rejects.toThrow('DRAFT_NOT_FOUND')
  })

  it('rejects amounts with more fractional digits than the asset supports', async () => {
    const api = mockApi()
    await api.unlock('test-password')
    await expect(api.prepare('demo', 'BTC', 'tb1qactualrecipient', '0.000000001')).rejects.toThrow('INVALID_SEND')
    expect(await api.history('demo')).toHaveLength(0)
  })
})
