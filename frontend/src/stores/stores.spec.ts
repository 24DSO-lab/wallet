import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

// The stores talk to a module-level `api` singleton (the in-memory mock under
// Vitest), so every test loads a fresh module graph and a fresh Pinia.
async function load() {
  vi.resetModules()
  setActivePinia(createPinia())
  const [{ useSessionStore }, { useWalletsStore }, { useSendStore }, { useUiStore }] = await Promise.all([
    import('./session'),
    import('./wallets'),
    import('./send'),
    import('./ui'),
  ])
  return { session: useSessionStore(), wallets: useWalletsStore(), send: useSendStore(), ui: useUiStore() }
}

describe('session store', () => {
  beforeEach(() => {
    vi.spyOn(globalThis.console, 'error').mockImplementation(() => undefined)
  })

  it('starts locked and holds no wallets after init', async () => {
    const { session, wallets } = await load()
    await session.init()

    expect(session.initializing).toBe(false)
    expect(session.locked).toBe(true)
    expect(wallets.wallets).toEqual([])
  })

  it('unlocks, loads wallets and selects the first one', async () => {
    const { session, wallets } = await load()
    await session.init()

    expect(await session.unlock('test-password')).toBe(true)
    expect(session.locked).toBe(false)
    expect(wallets.wallets.length).toBeGreaterThan(0)
    expect(wallets.current?.id).toBe(wallets.wallets[0]?.id)
  })

  it('stays locked and reports failure for an empty password', async () => {
    const { session, wallets } = await load()
    await session.init()

    expect(await session.unlock('')).toBe(false)
    expect(session.locked).toBe(true)
    expect(wallets.wallets).toEqual([])
  })

  it('drops wallet data and any pending send when locking', async () => {
    const { session, wallets, send } = await load()
    await session.init()
    await session.unlock('test-password')
    send.recipient = 'tb1qrecipient0000000000000000000000000000000'
    send.amount = '0.001'
    await send.prepare(wallets.selectedId)
    expect(send.quote).toBeDefined()

    await session.lock()

    expect(session.locked).toBe(true)
    expect(wallets.wallets).toEqual([])
    expect(wallets.selectedId).toBe('')
    expect(send.quote).toBeUndefined()
    expect(send.recipient).toBe('')
    expect(send.amount).toBe('')
  })
})

describe('send store', () => {
  it('prepares a quote and clears the flow after confirmation', async () => {
    const { session, wallets, send } = await load()
    await session.init()
    await session.unlock('test-password')

    send.asset = 'BTC'
    send.recipient = 'tb1qrecipient0000000000000000000000000000000'
    send.amount = '0.001'
    await send.prepare(wallets.selectedId)

    expect(send.quote?.asset).toBe('BTC')
    expect(send.quote?.amount).toBe('0.001')

    await send.confirm('test-password', '')
    expect(send.quote).toBeUndefined()
    expect(send.recipient).toBe('')
  })

  it('rejects an invalid amount without leaving a quote behind', async () => {
    const { session, wallets, send } = await load()
    await session.init()
    await session.unlock('test-password')

    send.recipient = 'tb1qrecipient0000000000000000000000000000000'
    send.amount = 'abc'
    await expect(send.prepare(wallets.selectedId)).rejects.toBeDefined()
    expect(send.quote).toBeUndefined()
  })

  it('ignores confirm when there is nothing prepared', async () => {
    const { send } = await load()
    await expect(send.confirm('x', '')).resolves.toBeUndefined()
  })
})

describe('wallets store', () => {
  it('removes the selected wallet and falls back to another', async () => {
    const { session, wallets } = await load()
    await session.init()
    await session.unlock('test-password')
    const first = wallets.wallets[0]
    if (!first) throw new Error('mock has no wallets')

    wallets.add({ id: 'extra', name: 'Extra', balances: [] })
    expect(wallets.selectedId).toBe('extra')

    wallets.remove('extra')
    expect(wallets.selectedId).toBe(first.id)
  })

  it('records a per-asset warning when a refresh fails', async () => {
    const { session, wallets } = await load()
    await session.init()
    await session.unlock('test-password')

    expect(await wallets.refreshAsset('missing-wallet', 'BTC')).toBe('failed')
    expect(wallets.warningsFor('missing-wallet', 'BTC')).toEqual(['refresh_failed'])
    expect(wallets.isAssetRefreshing('missing-wallet', 'BTC')).toBe(false)
  })
})
