import { describe, expect, it } from 'vitest'
import {
  validateAmount,
  validateEmail,
  validateFutureDate,
  validateInvoiceForm,
  validateLoginForm,
  validatePassword,
  validateRefundForm,
  validateRegisterForm,
  validateUUID,
} from './validation'

/**
 * SRS §4.5: email format, amount > 0, date ≥ today, clear messages.
 *
 * These rules mirror the Go `binding` tags. Where they diverge the backend wins, so the
 * tests below encode the *same* boundaries rather than stricter client-side guesses.
 */

describe('validateEmail', () => {
  it('accepts ordinary addresses', () => {
    for (const email of [
      'toko@example.com',
      'a.b+tag@sub.domain.co.id',
      'UPPER@Example.COM',
      'x@y.z',
    ]) {
      expect(validateEmail(email), email).toBeUndefined()
    }
  })

  it('rejects the shapes users actually mistype', () => {
    for (const email of ['', '   ', 'no-at-sign', 'no@domain', '@example.com', 'a b@c.com']) {
      expect(validateEmail(email), email).toBeDefined()
    }
  })

  it('trims before validating, so stray spaces are not an error on their own', () => {
    expect(validateEmail('  toko@example.com  ')).toBeUndefined()
  })

  it('enforces the 255 character limit from the DTO', () => {
    expect(validateEmail(`${'a'.repeat(250)}@example.com`)).toBeDefined()
  })
})

describe('validatePassword', () => {
  it('mirrors min=8,max=72', () => {
    expect(validatePassword('')).toBeDefined()
    expect(validatePassword('short')).toBeDefined()
    expect(validatePassword('a'.repeat(7))).toBeDefined()
    expect(validatePassword('a'.repeat(8))).toBeUndefined()
    expect(validatePassword('a'.repeat(72))).toBeUndefined()
    // 72 is bcrypt's input limit; beyond it the tail is silently ignored, so the API
    // rejects it rather than accepting a password that is not fully checked.
    expect(validatePassword('a'.repeat(73))).toBeDefined()
  })
})

describe('validateAmount', () => {
  it('requires a positive integer', () => {
    expect(validateAmount('1')).toBeUndefined()
    expect(validateAmount('50000')).toBeUndefined()
  })

  it('rejects zero and negatives (SRS §4.5)', () => {
    for (const value of ['0', '-1', '-50000']) {
      expect(validateAmount(value), value).toBeDefined()
    }
  })

  it('rejects fractions, because the API takes whole rupiah', () => {
    // Silently rounding would change the amount the user asked for.
    expect(validateAmount('100.5')).toBeDefined()
    expect(validateAmount('0.99')).toBeDefined()
  })

  it('rejects empty and non-numeric input', () => {
    for (const value of ['', '   ', 'abc', '1e', '--5']) {
      expect(validateAmount(value), value).toBeDefined()
    }
  })

  it('rejects amounts beyond safe integer precision', () => {
    // Past 2^53 a JS number can no longer represent every integer, so the value that
    // reached the server would not be the value shown.
    expect(validateAmount('9007199254740993')).toBeDefined()
  })

  it('enforces an optional maximum', () => {
    expect(validateAmount('1000', { max: 1000 })).toBeUndefined()
    expect(validateAmount('1001', { max: 1000 })).toBeDefined()
  })
})

describe('validateFutureDate', () => {
  const today = '2026-07-26'

  it('accepts today and later', () => {
    expect(validateFutureDate(today, today)).toBeUndefined()
    expect(validateFutureDate('2026-07-27', today)).toBeUndefined()
    expect(validateFutureDate('2027-01-01', today)).toBeUndefined()
  })

  it('rejects yesterday', () => {
    expect(validateFutureDate('2026-07-25', today)).toBeDefined()
    expect(validateFutureDate('2025-12-31', today)).toBeDefined()
  })

  it('requires a value and a well-formed date', () => {
    expect(validateFutureDate('', today)).toBeDefined()
    expect(validateFutureDate('26-07-2026', today)).toBeDefined()
    expect(validateFutureDate('not-a-date', today)).toBeDefined()
  })

  /**
   * Comparison is on yyyy-MM-dd strings in the user's own calendar, deliberately. Building
   * Date objects would drag in time-of-day and time zones, and "today" for a user at UTC+7
   * is not the same instant as "today" in UTC — a naive check rejects a same-day due date
   * for everyone east of Greenwich.
   */
  it('treats the boundary as a calendar day, not an instant', () => {
    expect(validateFutureDate('2026-01-01', '2026-01-01')).toBeUndefined()
    expect(validateFutureDate('2025-12-31', '2026-01-01')).toBeDefined()
  })
})

describe('validateUUID', () => {
  it('accepts a canonical UUID in either case', () => {
    expect(validateUUID('123e4567-e89b-12d3-a456-426614174000')).toBeUndefined()
    expect(validateUUID('123E4567-E89B-12D3-A456-426614174000')).toBeUndefined()
  })

  it('rejects anything else', () => {
    for (const value of ['', 'abc', '123e4567e89b12d3a456426614174000', '123e4567-e89b-12d3-a456']) {
      expect(validateUUID(value), value).toBeDefined()
    }
  })

  it('names the field in the message so the user knows which input to fix', () => {
    expect(validateUUID('nope', 'Merchant ID')).toContain('Merchant ID')
  })
})

// ── form-level ────────────────────────────────────────────────────────────────

describe('validateLoginForm', () => {
  it('passes with an email and any non-empty password', () => {
    const r = validateLoginForm({ email: 'toko@example.com', password: 'x' })
    expect(r.valid).toBe(true)
  })

  /**
   * Login intentionally does not apply the length rules. Telling a caller their password is
   * "too short" reveals which guesses are the wrong *shape*, and length is a registration
   * concern anyway.
   */
  it('does not apply length rules to the password', () => {
    const r = validateLoginForm({ email: 'toko@example.com', password: 'abc' })
    expect(r.valid).toBe(true)
    expect(r.errors.password).toBeUndefined()
  })

  it('collects an error per invalid field', () => {
    const r = validateLoginForm({ email: 'bad', password: '' })
    expect(r.valid).toBe(false)
    expect(r.errors.email).toBeDefined()
    expect(r.errors.password).toBeDefined()
  })
})

describe('validateRegisterForm', () => {
  it('passes on valid input', () => {
    expect(
      validateRegisterForm({ name: 'Toko A', email: 'a@b.com', password: 'password123' }).valid,
    ).toBe(true)
  })

  it('enforces the name bounds from the DTO', () => {
    expect(validateRegisterForm({ name: 'A', email: 'a@b.com', password: 'password123' }).errors.name).toBeDefined()
    expect(
      validateRegisterForm({ name: 'a'.repeat(101), email: 'a@b.com', password: 'password123' })
        .errors.name,
    ).toBeDefined()
  })

  it('applies the full password policy, unlike login', () => {
    expect(
      validateRegisterForm({ name: 'Toko', email: 'a@b.com', password: 'short' }).errors.password,
    ).toBeDefined()
  })
})

describe('validateInvoiceForm', () => {
  const today = '2026-07-26'
  const valid = {
    customer_name: 'Budi',
    customer_email: '',
    description: '',
    amount: '50000',
    due_date: '2026-08-01',
  }

  it('passes with only the required fields', () => {
    expect(validateInvoiceForm(valid, today).valid).toBe(true)
  })

  it('treats customer email as optional but validates it when present', () => {
    expect(validateInvoiceForm({ ...valid, customer_email: '' }, today).valid).toBe(true)
    expect(
      validateInvoiceForm({ ...valid, customer_email: 'not-an-email' }, today).errors
        .customer_email,
    ).toBeDefined()
    expect(validateInvoiceForm({ ...valid, customer_email: 'ok@x.com' }, today).valid).toBe(true)
  })

  it('requires a customer name', () => {
    expect(validateInvoiceForm({ ...valid, customer_name: '   ' }, today).errors.customer_name).toBeDefined()
  })

  it('applies the amount and date rules', () => {
    expect(validateInvoiceForm({ ...valid, amount: '0' }, today).errors.amount).toBeDefined()
    expect(validateInvoiceForm({ ...valid, due_date: '2026-07-25' }, today).errors.due_date).toBeDefined()
  })

  it('caps the description at 500 characters', () => {
    expect(
      validateInvoiceForm({ ...valid, description: 'a'.repeat(501) }, today).errors.description,
    ).toBeDefined()
    expect(validateInvoiceForm({ ...valid, description: 'a'.repeat(500) }, today).valid).toBe(true)
  })

  it('reports every problem at once rather than one at a time', () => {
    // A form that reveals errors one submit at a time is miserable to fill in.
    const r = validateInvoiceForm(
      { customer_name: '', customer_email: 'bad', description: '', amount: '-5', due_date: '' },
      today,
    )
    expect(Object.keys(r.errors).length).toBeGreaterThanOrEqual(4)
  })
})

describe('validateRefundForm', () => {
  const valid = {
    invoice_id: '123e4567-e89b-12d3-a456-426614174000',
    amount: '1000',
    reason: '',
  }

  it('passes on valid input', () => {
    expect(validateRefundForm(valid).valid).toBe(true)
  })

  it('requires a UUID invoice id', () => {
    expect(validateRefundForm({ ...valid, invoice_id: 'nope' }).errors.invoice_id).toBeDefined()
  })

  /**
   * maxAmount is only the invoice total — an upper bound the client can see. The real limit
   * is the cumulative total across every in-flight and settled refund, which only the
   * server can compute, so passing this check does not guarantee the request succeeds.
   */
  it('honours maxAmount when supplied', () => {
    expect(validateRefundForm({ ...valid, amount: '1000' }, { maxAmount: 1000 }).valid).toBe(true)
    expect(
      validateRefundForm({ ...valid, amount: '1001' }, { maxAmount: 1000 }).errors.amount,
    ).toBeDefined()
  })

  it('does not bound the amount when no maximum is known', () => {
    expect(validateRefundForm({ ...valid, amount: '999999999' }).valid).toBe(true)
  })

  it('caps the reason at 500 characters', () => {
    expect(validateRefundForm({ ...valid, reason: 'a'.repeat(501) }).errors.reason).toBeDefined()
  })
})

describe('error messages', () => {
  it('are in Indonesian and say what to do', () => {
    // SRS §4.5 asks for clear messages; a bare "invalid" is not actionable.
    expect(validateEmail('nope')).toMatch(/format email tidak valid/i)
    expect(validateAmount('0')).toMatch(/lebih besar dari 0/i)
    expect(validateFutureDate('2020-01-01', '2026-07-26')).toMatch(/masa lalu/i)
    expect(validatePassword('abc')).toMatch(/minimal 8 karakter/i)
  })
})
