import { describe, expect, it } from 'vitest'
import { errorCode, sendErrorKey } from './errors'

describe('sendErrorKey', () => {
  it('maps known Go error codes', () => {
    expect(sendErrorKey(new Error('invalid_address'))).toBe('send.errors.invalidAddress')
    expect(sendErrorKey('insufficient_funds')).toBe('send.errors.insufficientFunds')
    expect(sendErrorKey(new Error('insufficient_fee_coin'))).toBe('send.errors.insufficientFee')
    expect(sendErrorKey(new Error('duplicate_send'))).toBe('send.duplicate')
  })

  it('does not confuse insufficient_funds with insufficient_fee_coin', () => {
    expect(sendErrorKey('insufficient_fee_coin')).not.toBe('send.errors.insufficientFunds')
  })

  it('maps prepare failures to readable messages', () => {
    expect(sendErrorKey(new Error('verification_failed'))).toBe('send.errors.verificationFailed')
    expect(sendErrorKey(new Error('timeout'))).toBe('send.errors.timeout')
  })

  it('falls back for unknown or mock errors', () => {
    expect(sendErrorKey(new Error('INVALID_SEND'))).toBe('send.invalid')
    expect(sendErrorKey(undefined, 'x')).toBe('x')
  })
})

describe('errorCode', () => {
  it('extracts the stable Go code', () => {
    expect(errorCode(new Error('wrong_password'))).toBe('wrong_password')
    expect(errorCode('locked')).toBe('locked')
  })

  it('falls back to unknown', () => {
    expect(errorCode(new Error('something else'))).toBe('unknown')
  })

  it('passes through any bare snake_case Go code', () => {
    expect(errorCode(new Error('verification_failed'))).toBe('verification_failed')
  })
})
