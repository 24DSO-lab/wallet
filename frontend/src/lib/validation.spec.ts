import { describe, expect, it } from 'vitest'
import { checkSendAmount, mnemonicWordCount } from './validation'

describe('checkSendAmount', () => {
  it('accepts decimals and a decimal comma', () => {
    expect(checkSendAmount('0.001', 8)).toEqual({ ok: true, value: '0.001' })
    expect(checkSendAmount(' 1,5 ', 8)).toEqual({ ok: true, value: '1.5' })
    expect(checkSendAmount('2', 6)).toEqual({ ok: true, value: '2' })
  })

  it('rejects zero, malformed and negative values', () => {
    expect(checkSendAmount('0', 8)).toEqual({ ok: false, reason: 'zero' })
    expect(checkSendAmount('0.000', 8)).toEqual({ ok: false, reason: 'zero' })
    expect(checkSendAmount('', 8)).toEqual({ ok: false, reason: 'invalid' })
    expect(checkSendAmount('-1', 8)).toEqual({ ok: false, reason: 'invalid' })
    expect(checkSendAmount('1e3', 8)).toEqual({ ok: false, reason: 'invalid' })
    expect(checkSendAmount('1.', 8)).toEqual({ ok: false, reason: 'invalid' })
  })

  it('rejects more fractional digits than the asset supports', () => {
    expect(checkSendAmount('0.123456789', 8)).toEqual({ ok: false, reason: 'precision' })
    expect(checkSendAmount('1.1234567', 6)).toEqual({ ok: false, reason: 'precision' })
    expect(checkSendAmount('0.12345678', 8).ok).toBe(true)
  })
})

describe('mnemonicWordCount', () => {
  it('counts words regardless of whitespace', () => {
    expect(mnemonicWordCount('')).toBe(0)
    expect(mnemonicWordCount('  a  b\nc ')).toBe(3)
  })
})
