/** Go enforces the same minimum; this only gives instant feedback in forms. */
export const MIN_PASSWORD_LENGTH = 10

export const MNEMONIC_WORD_COUNTS: readonly number[] = [12, 24]

export function mnemonicWordCount(phrase: string): number {
  const trimmed = phrase.trim()
  return trimmed ? trimmed.split(/\s+/).length : 0
}

export type AmountCheck =
  | { ok: true; value: string }
  | { ok: false; reason: 'invalid' | 'zero' | 'precision' }

/**
 * Instant feedback for the send form. Accepts a decimal comma, rejects zero
 * and more fractional digits than the asset supports. Go repeats every check
 * and stays the source of truth.
 */
export function checkSendAmount(raw: string, decimals: number): AmountCheck {
  const value = raw.trim().replace(',', '.')
  if (!/^\d+(\.\d+)?$/.test(value)) return { ok: false, reason: 'invalid' }
  if (!/[1-9]/.test(value)) return { ok: false, reason: 'zero' }
  const fraction = value.split('.')[1] ?? ''
  if (fraction.length > decimals) return { ok: false, reason: 'precision' }
  return { ok: true, value }
}
