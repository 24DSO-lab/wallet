/** Go enforces the same minimum; this only gives instant feedback in forms. */
export const MIN_PASSWORD_LENGTH = 10

export const MNEMONIC_WORD_COUNTS: readonly number[] = [12, 24]

export function mnemonicWordCount(phrase: string): number {
  const trimmed = phrase.trim()
  return trimmed ? trimmed.split(/\s+/).length : 0
}
