import { describe, expect, it } from 'vitest'
import { formatAmount, parseAmount } from './amount'

describe('exact amounts', () => {
  it('handles zero and satoshi', () => {
    expect(formatAmount('0', 8)).toBe('0')
    expect(formatAmount('1', 8)).toBe('0.00000001')
  })

  it('handles wei and huge balances', () => {
    expect(formatAmount('1', 18)).toBe('0.000000000000000001')
    expect(formatAmount('123456789012345678901234567890', 18)).toBe('123456789012.34567890123456789')
  })

  it('trims trailing zeros of the fraction', () => {
    expect(formatAmount('125000000', 8)).toBe('1.25')
    expect(formatAmount('100000000', 8)).toBe('1')
  })

  it('rejects malformed input', () => {
    expect(formatAmount('-1', 8)).toBe('—')
    expect(formatAmount('1.5', 8)).toBe('—')
    expect(formatAmount('1', 37)).toBe('—')
    expect(formatAmount('1', 1.5)).toBe('—')
  })

  it('parses exactly', () => {
    expect(parseAmount('1.00000001', 8)).toBe('100000001')
    expect(parseAmount('0.000000000000000001', 18)).toBe('1')
    expect(parseAmount('2', 6)).toBe('2000000')
  })

  it('rejects excess precision and malformed values', () => {
    expect(() => parseAmount('1.000000001', 8)).toThrow('TOO_MANY_DECIMALS')
    expect(() => parseAmount('abc', 8)).toThrow('INVALID_AMOUNT')
    expect(() => parseAmount('-1', 8)).toThrow('INVALID_AMOUNT')
  })
})
