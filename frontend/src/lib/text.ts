/** Shortens a long identifier (address, contract) to `head…tail` for compact display. */
export function shortId(value: string, head = 6, tail = 6): string {
  return value.length > head + tail + 1 ? `${value.slice(0, head)}…${value.slice(-tail)}` : value
}
