import { describe, expect, it } from 'vitest'
import { clearPendingIntent, getPendingIntent, setPendingIntent } from './pendingIntent'

describe('pendingIntent', () => {
  it('returns null for a token nothing was stored under', () => {
    expect(getPendingIntent('unknown-token')).toBeNull()
  })

  it('round-trips what was stored', () => {
    setPendingIntent('token-a', 'intent-1')
    expect(getPendingIntent('token-a')).toBe('intent-1')
  })

  it('keeps tokens independent, so restoring one payment link cannot surface another', () => {
    setPendingIntent('token-a', 'intent-1')
    setPendingIntent('token-b', 'intent-2')

    expect(getPendingIntent('token-a')).toBe('intent-1')
    expect(getPendingIntent('token-b')).toBe('intent-2')
  })

  it('overwrites a previous intent for the same token', () => {
    setPendingIntent('token-a', 'intent-1')
    setPendingIntent('token-a', 'intent-2')

    expect(getPendingIntent('token-a')).toBe('intent-2')
  })

  it('clearing one token leaves others untouched', () => {
    setPendingIntent('token-a', 'intent-1')
    setPendingIntent('token-b', 'intent-2')

    clearPendingIntent('token-a')

    expect(getPendingIntent('token-a')).toBeNull()
    expect(getPendingIntent('token-b')).toBe('intent-2')
  })

  it('clearing a token that was never stored is a no-op, not an error', () => {
    expect(() => clearPendingIntent('never-stored')).not.toThrow()
  })

  it('treats corrupt JSON in storage as nothing stored, rather than throwing', () => {
    window.localStorage.setItem('payment-sandbox.pending-intents', '{not json')
    expect(getPendingIntent('token-a')).toBeNull()
  })

  it('survives storage that rejects writes (e.g. private-browsing quota)', () => {
    const original = window.localStorage.setItem.bind(window.localStorage)
    window.localStorage.setItem = () => {
      throw new DOMException('QuotaExceededError')
    }
    try {
      expect(() => setPendingIntent('token-a', 'intent-1')).not.toThrow()
    } finally {
      window.localStorage.setItem = original
    }
  })
})
