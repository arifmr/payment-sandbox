/**
 * Form validation (SRS §4.5: email format, amount > 0, date ≥ today, clear messages).
 *
 * These rules mirror the `binding` tags on the Go DTOs so the user gets an immediate,
 * specific message instead of a round-trip. They are a **usability** layer, never a
 * security one: the backend validates independently and is the authority. Anything
 * enforced only here is not enforced.
 *
 * Messages are in Indonesian, phrased to say what to do rather than what went wrong.
 */

export type FieldErrors<T> = Partial<Record<keyof T, string>>

/** A validated form: either the errors to show, or nothing. */
export interface ValidationResult<T> {
  errors: FieldErrors<T>
  valid: boolean
}

function result<T>(errors: FieldErrors<T>): ValidationResult<T> {
  return { errors, valid: Object.keys(errors).length === 0 }
}

// ── primitives ────────────────────────────────────────────────────────────────

/**
 * Deliberately permissive email check: one @, something either side, a dot in the domain.
 * Stricter regexes reject valid addresses, and the real verdict comes from the backend's
 * validator anyway. The purpose here is catching typos like a missing @.
 */
const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

export function validateEmail(value: string): string | undefined {
  const email = value.trim()
  if (!email) return 'Email wajib diisi.'
  if (!EMAIL_RE.test(email)) return 'Format email tidak valid, contoh: nama@domain.com.'
  if (email.length > 255) return 'Email maksimal 255 karakter.'
  return undefined
}

/** Mirrors `min=8,max=72` — 72 is bcrypt's input limit, beyond which it silently truncates. */
export function validatePassword(value: string): string | undefined {
  if (!value) return 'Password wajib diisi.'
  if (value.length < 8) return 'Password minimal 8 karakter.'
  if (value.length > 72) return 'Password maksimal 72 karakter.'
  return undefined
}

export function validateName(value: string): string | undefined {
  const name = value.trim()
  if (!name) return 'Nama wajib diisi.'
  if (name.length < 2) return 'Nama minimal 2 karakter.'
  if (name.length > 100) return 'Nama maksimal 100 karakter.'
  return undefined
}

/**
 * SRS §4.5: amount must be > 0.
 *
 * The input arrives as a string, so this also rejects the shapes a number input cannot:
 * empty, non-numeric, and fractional. Fractions are rejected because the API takes whole
 * rupiah as an integer — silently rounding would change what the user asked for.
 */
export function validateAmount(value: string, opts: { max?: number } = {}): string | undefined {
  const raw = value.trim()
  if (!raw) return 'Nominal wajib diisi.'

  const amount = Number(raw)
  if (!Number.isFinite(amount)) return 'Nominal harus berupa angka.'
  if (!Number.isInteger(amount)) return 'Nominal harus bilangan bulat (tanpa desimal).'
  if (amount <= 0) return 'Nominal harus lebih besar dari 0.'
  if (!Number.isSafeInteger(amount)) return 'Nominal terlalu besar.'
  if (opts.max !== undefined && amount > opts.max) {
    return `Nominal melebihi batas maksimal ${opts.max.toLocaleString('id-ID')}.`
  }
  return undefined
}

/**
 * SRS §4.5: date must be ≥ today.
 *
 * Compared as `yyyy-MM-dd` strings in the user's own calendar. Comparing Date objects
 * would drag in time-of-day and time zones, and "today" for a user in UTC+7 is not the
 * same instant as "today" in UTC — a naive check rejects a same-day due date for anyone
 * east of Greenwich.
 */
export function validateFutureDate(value: string, today: string): string | undefined {
  if (!value) return 'Tanggal jatuh tempo wajib diisi.'
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return 'Format tanggal tidak valid.'
  if (value < today) return 'Tanggal jatuh tempo tidak boleh di masa lalu.'
  return undefined
}

const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

export function validateUUID(value: string, label = 'ID'): string | undefined {
  if (!value.trim()) return `${label} wajib diisi.`
  if (!UUID_RE.test(value.trim())) return `${label} bukan UUID yang valid.`
  return undefined
}

export function validateMaxLength(
  value: string,
  max: number,
  label: string,
): string | undefined {
  if (value.length > max) return `${label} maksimal ${max} karakter.`
  return undefined
}

// ── form validators ───────────────────────────────────────────────────────────

export interface LoginForm {
  email: string
  password: string
}

export function validateLoginForm(form: LoginForm): ValidationResult<LoginForm> {
  const errors: FieldErrors<LoginForm> = {}
  const email = validateEmail(form.email)
  if (email) errors.email = email
  // Login only needs "not empty" — length rules belong to registration. Applying them
  // here would tell an attacker which passwords are the wrong *shape*.
  if (!form.password) errors.password = 'Password wajib diisi.'
  return result(errors)
}

export interface RegisterForm {
  name: string
  email: string
  password: string
}

export function validateRegisterForm(form: RegisterForm): ValidationResult<RegisterForm> {
  const errors: FieldErrors<RegisterForm> = {}
  const name = validateName(form.name)
  if (name) errors.name = name
  const email = validateEmail(form.email)
  if (email) errors.email = email
  const password = validatePassword(form.password)
  if (password) errors.password = password
  return result(errors)
}

export interface InvoiceForm {
  customer_name: string
  customer_email: string
  description: string
  amount: string
  due_date: string
}

export function validateInvoiceForm(
  form: InvoiceForm,
  today: string,
): ValidationResult<InvoiceForm> {
  const errors: FieldErrors<InvoiceForm> = {}

  const name = form.customer_name.trim()
  if (!name) errors.customer_name = 'Nama pelanggan wajib diisi.'
  else {
    const tooLong = validateMaxLength(name, 255, 'Nama pelanggan')
    if (tooLong) errors.customer_name = tooLong
  }

  // Optional, but must be well-formed when provided (`omitempty,email` on the DTO).
  if (form.customer_email.trim()) {
    const email = validateEmail(form.customer_email)
    if (email) errors.customer_email = email
  }

  const description = validateMaxLength(form.description, 500, 'Deskripsi')
  if (description) errors.description = description

  const amount = validateAmount(form.amount)
  if (amount) errors.amount = amount

  const due = validateFutureDate(form.due_date, today)
  if (due) errors.due_date = due

  return result(errors)
}

export interface TopupForm {
  amount: string
}

export function validateTopupForm(form: TopupForm): ValidationResult<TopupForm> {
  const errors: FieldErrors<TopupForm> = {}
  const amount = validateAmount(form.amount)
  if (amount) errors.amount = amount
  return result(errors)
}

export interface RefundForm {
  invoice_id: string
  amount: string
  reason: string
}

/**
 * `maxAmount` is the invoice's remaining refundable balance when the caller knows it.
 * The backend caps the cumulative total across every in-flight and settled refund, which
 * the client cannot compute on its own — so this catches the obvious case early and
 * defers to the server for the real limit.
 */
export function validateRefundForm(
  form: RefundForm,
  opts: { maxAmount?: number } = {},
): ValidationResult<RefundForm> {
  const errors: FieldErrors<RefundForm> = {}

  const id = validateUUID(form.invoice_id, 'Invoice')
  if (id) errors.invoice_id = id

  const amount = validateAmount(
    form.amount,
    opts.maxAmount === undefined ? {} : { max: opts.maxAmount },
  )
  if (amount) errors.amount = amount

  const reason = validateMaxLength(form.reason, 500, 'Alasan')
  if (reason) errors.reason = reason

  return result(errors)
}
