/**
 * Go reports failures as stable machine codes (domain.ErrorCode) and never
 * includes secrets or provider details. This maps those codes to i18n keys.
 */
const CODE_KEYS: Readonly<Record<string, string>> = {
  invalid_address: 'send.errors.invalidAddress',
  invalid_amount: 'send.errors.invalidAmount',
  wrong_network: 'send.errors.wrongNetwork',
  insufficient_funds: 'send.errors.insufficientFunds',
  insufficient_fee_coin: 'send.errors.insufficientFee',
  provider_unavailable: 'send.errors.providerUnavailable',
  provider_rate_limited: 'send.errors.providerRateLimited',
  provider_bad_response: 'send.errors.providerUnavailable',
  duplicate_send: 'send.duplicate',
  timeout: 'send.errors.timeout',
  canceled: 'send.errors.timeout',
  verification_failed: 'send.errors.verificationFailed',
  draft_expired: 'send.errors.draftExpired',
  not_found: 'send.errors.notFound',
  session_locked: 'send.errors.locked',
  locked: 'send.errors.locked',
}

const KNOWN_CODES = [
  'wrong_password', 'invalid_input', 'not_found', 'locked', 'session_locked', 'canceled', 'timeout', 'internal',
] as const

/** Returns the stable Go error code found in `error`, or "unknown". Never contains secrets. */
export function errorCode(error: unknown): string {
  const text = error instanceof Error ? error.message : String(error)
  const known = KNOWN_CODES.find(code => text.includes(code))
  if (known) return known
  // Go sends nothing but the code (snake_case), so a bare code is safe to show.
  return /^[a-z]+(_[a-z]+)*$/.test(text) && text.length <= 40 ? text : 'unknown'
}

/** Returns the i18n key for the first known error code in `error`, or `fallback`. */
export function sendErrorKey(error: unknown, fallback = 'send.invalid'): string {
  const text = error instanceof Error ? error.message : String(error)
  const code = Object.keys(CODE_KEYS).find(candidate => text.includes(candidate))
  return code ? (CODE_KEYS[code] ?? fallback) : fallback
}
