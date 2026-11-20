const MAX_DECIMALS = 36

/**
 * Formats an integer amount in base units (satoshi, wei, ...) as a decimal
 * string using BigInt only, so large balances never lose precision.
 * Returns an em dash for input that is not a valid amount.
 */
export function formatAmount(raw: string, decimals: number): string {
  if (!/^\d+$/.test(raw) || decimals % 1 !== 0 || decimals < 0 || decimals > MAX_DECIMALS) return '—'
  const value = BigInt(raw)
  const base = 10n ** BigInt(decimals)
  const whole = value / base
  const fraction = (value % base).toString().padStart(decimals, '0').replace(/0+$/, '')
  return fraction ? `${whole}.${fraction}` : whole.toString()
}

/**
 * Parses a user-entered decimal amount into base units. Throws
 * `INVALID_AMOUNT` for malformed input and `TOO_MANY_DECIMALS` when the
 * precision exceeds what the asset supports.
 */
export function parseAmount(value: string, decimals: number): string {
  if (!/^\d+(\.\d+)?$/.test(value)) throw new Error('INVALID_AMOUNT')
  const [whole = '0', fraction = ''] = value.split('.')
  if (fraction.length > decimals) throw new Error('TOO_MANY_DECIMALS')
  const scaled = BigInt(whole) * 10n ** BigInt(decimals) + BigInt(fraction.padEnd(decimals, '0') || '0')
  return scaled.toString()
}
