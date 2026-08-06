import { describe, expect, it } from 'vitest'
import {
  dateInputToRFC3339,
  formatAmount,
  formatDate,
  formatDateTime,
  formatMoney,
  methodLabel,
  shortId,
  statusLabel,
  toDateInputValue,
  toISODate,
  todayISODate,
} from './format'

describe('formatMoney', () => {
  it('renders whole rupiah with grouping', () => {
    // Intl inserts a non-breaking space after "Rp", so assert on the digits rather than
    // the exact spacing, which varies by ICU version.
    expect(formatMoney(50000)).toMatch(/Rp\s?50\.000/)
    expect(formatMoney(0)).toMatch(/Rp\s?0/)
  })

  it('has no fraction digits, because amounts are integer rupiah', () => {
    expect(formatMoney(1500)).not.toContain(',')
  })

  it('handles negative values, which appear as wallet debits', () => {
    expect(formatMoney(-2000)).toMatch(/2\.000/)
  })

  it('formats large amounts without losing grouping', () => {
    expect(formatAmount(1234567)).toBe('1.234.567')
  })
})

describe('formatDateTime / formatDate', () => {
  it('renders a valid timestamp', () => {
    const out = formatDateTime('2026-07-26T10:30:00Z')
    expect(out).not.toBe('—')
    expect(out.length).toBeGreaterThan(5)
  })

  /**
   * Missing values render as an em dash rather than an empty cell, so table columns stay
   * visibly aligned and a blank does not read as "zero".
   */
  it('renders a dash for missing or unparseable values', () => {
    for (const value of [null, undefined, '', 'not-a-date']) {
      expect(formatDateTime(value), String(value)).toBe('—')
      expect(formatDate(value), String(value)).toBe('—')
    }
  })
})

describe('toISODate', () => {
  /**
   * Uses local calendar parts, not toISOString(). toISOString() converts to UTC first, so
   * for a user at UTC+7 any local time before 07:00 would come back as the *previous* day —
   * which would make "today" fail the due-date check.
   */
  it('uses local calendar parts', () => {
    const d = new Date(2026, 0, 1, 2, 0, 0) // 1 Jan, 02:00 local
    expect(toISODate(d)).toBe('2026-01-01')
  })

  it('zero-pads month and day', () => {
    expect(toISODate(new Date(2026, 8, 5))).toBe('2026-09-05')
  })

  it('todayISODate matches the local calendar day', () => {
    expect(todayISODate()).toBe(toISODate(new Date()))
  })

  it('round-trips through a date input value', () => {
    const iso = new Date(2026, 5, 15, 12, 0, 0).toISOString()
    expect(toDateInputValue(iso)).toBe('2026-06-15')
  })

  it('returns empty for an unparseable input', () => {
    expect(toDateInputValue('rubbish')).toBe('')
  })
})

describe('dateInputToRFC3339', () => {
  /**
   * The chosen day is pushed to its final second. A due date of "today" must mean
   * end-of-today: midnight has already passed, and the backend rejects a past due date, so
   * naive midnight conversion makes same-day invoices impossible to create.
   */
  it('maps a calendar day to the end of that day', () => {
    const out = dateInputToRFC3339('2026-08-01')
    const parsed = new Date(out)
    expect(parsed.getFullYear()).toBe(2026)
    expect(parsed.getMonth()).toBe(7)
    expect(parsed.getDate()).toBe(1)
    expect(parsed.getHours()).toBe(23)
    expect(parsed.getMinutes()).toBe(59)
  })

  it('produces a timestamp in the future for today', () => {
    expect(new Date(dateInputToRFC3339(todayISODate())).getTime()).toBeGreaterThan(Date.now())
  })

  it('returns empty for malformed input rather than an Invalid Date string', () => {
    for (const value of ['', 'nope', '2026-08']) {
      expect(dateInputToRFC3339(value), value).toBe('')
    }
  })
})

describe('labels', () => {
  it('translates every status the API can return', () => {
    const statuses = [
      'PENDING',
      'PAID',
      'EXPIRED',
      'SUCCESS',
      'FAILED',
      'REQUESTED',
      'APPROVED',
      'REJECTED',
    ]
    for (const s of statuses) {
      const label = statusLabel(s)
      expect(label, s).not.toBe(s) // must actually be translated
      expect(label.length, s).toBeGreaterThan(0)
    }
  })

  it('falls back to the raw code for an unknown status', () => {
    // A new backend status must show *something* rather than an empty badge.
    expect(statusLabel('BRAND_NEW')).toBe('BRAND_NEW')
  })

  it('translates every payment method', () => {
    for (const m of ['WALLET', 'VA_DUMMY', 'EWALLET_DUMMY']) {
      expect(methodLabel(m), m).not.toBe(m)
    }
    expect(methodLabel('UNKNOWN')).toBe('UNKNOWN')
  })
})

describe('shortId', () => {
  it('truncates a UUID for display', () => {
    const id = '123e4567-e89b-12d3-a456-426614174000'
    expect(shortId(id)).toBe('123e4567…')
  })

  it('leaves already-short values alone', () => {
    expect(shortId('abc')).toBe('abc')
    expect(shortId('12345678')).toBe('12345678')
  })
})
