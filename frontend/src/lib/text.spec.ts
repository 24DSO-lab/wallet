import { describe, expect, it } from 'vitest'
import { shortId } from './text'

describe('shortId', () => {
  it('keeps the start and the end of a long identifier', () => {
    expect(shortId('0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238')).toBe('0x1c7D…9C7238')
  })

  it('leaves short values untouched', () => {
    expect(shortId('abc')).toBe('abc')
  })
})
