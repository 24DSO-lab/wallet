import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { NOTICE_TIMEOUT_MS, useUiStore } from './ui'

describe('ui store', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    setActivePinia(createPinia())
  })
  afterEach(() => vi.useRealTimers())

  it('clears a notice after the timeout but keeps errors', async () => {
    const ui = useUiStore()
    ui.notice = 'saved'
    ui.error = 'failed'
    await nextTick()

    vi.advanceTimersByTime(NOTICE_TIMEOUT_MS - 1)
    expect(ui.notice).toBe('saved')
    vi.advanceTimersByTime(1)
    expect(ui.notice).toBe('')
    expect(ui.error).toBe('failed')
  })

  it('restarts the timer when the notice changes', async () => {
    const ui = useUiStore()
    ui.notice = 'first'
    await nextTick()
    vi.advanceTimersByTime(NOTICE_TIMEOUT_MS - 1000)
    ui.notice = 'second'
    await nextTick()
    vi.advanceTimersByTime(NOTICE_TIMEOUT_MS - 1000)
    expect(ui.notice).toBe('second')
    vi.advanceTimersByTime(1000)
    expect(ui.notice).toBe('')
  })

  it('clear() empties both messages', async () => {
    const ui = useUiStore()
    ui.notice = 'a'
    ui.error = 'b'
    ui.clear()
    expect(ui.notice).toBe('')
    expect(ui.error).toBe('')
  })
})
