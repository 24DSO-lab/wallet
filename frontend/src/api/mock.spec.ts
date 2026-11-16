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
