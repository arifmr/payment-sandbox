/**
 * Display formatting. Kept in one place so a currency or date change is a single edit
 * rather than a search through JSX.
 */

const IDR = new Intl.NumberFormat('id-ID', {
  style: 'currency',
  currency: 'IDR',
  maximumFractionDigits: 0,
})

/**
 * Formats a rupiah amount.
 *
 * Amounts cross the wire as integers, not decimals: floating point cannot represent all
 * decimal fractions exactly, and "close enough" is not a property money may have. The Go
 * side stores BIGINT for the same reason. IDR has no minor unit in practice, so the
 * integer is whole rupiah.
 */
export function formatMoney(amount: number): string {
  return IDR.format(amount)
}

/** Digit grouping without the currency symbol, for inputs and compact table cells. */
export function formatAmount(amount: number): string {
  return new Intl.NumberFormat('id-ID').format(amount)
}

const DATE_TIME = new Intl.DateTimeFormat('id-ID', {
  dateStyle: 'medium',
  timeStyle: 'short',
})

const DATE_ONLY = new Intl.DateTimeFormat('id-ID', { dateStyle: 'medium' })

/** Formats an RFC3339 timestamp. Returns '—' for missing values so tables stay aligned. */
export function formatDateTime(iso: string | null | undefined): string {
  if (!iso) return '—'
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '—' : DATE_TIME.format(d)
}

export function formatDate(iso: string | null | undefined): string {
  if (!iso) return '—'
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '—' : DATE_ONLY.format(d)
}

/** Turns an RFC3339 timestamp into the `yyyy-MM-dd` a date input expects. */
export function toDateInputValue(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return toISODate(d)
}

/** Local-calendar `yyyy-MM-dd`. Avoids toISOString(), which shifts to UTC and can slip a day. */
export function toISODate(date: Date): string {
  const y = date.getFullYear()
  const m = String(date.getMonth() + 1).padStart(2, '0')
  const d = String(date.getDate()).padStart(2, '0')
  return `${y}-${m}-${d}`
}

/** Today in the user's own calendar, for `min` on date inputs (SRS §4.5). */
export function todayISODate(): string {
  return toISODate(new Date())
}

/**
 * Converts a `yyyy-MM-dd` date input into the RFC3339 the API expects.
 *
 * The time is pushed to the end of the chosen day. A due date of "today" must mean
 * end-of-today, not midnight — midnight has already passed, and the backend rejects a
 * due date in the past.
 */
export function dateInputToRFC3339(value: string): string {
  const [y, m, d] = value.split('-').map(Number)
  if (!y || !m || !d) return ''
  return new Date(y, m - 1, d, 23, 59, 59).toISOString()
}

/** Human label for a status code, used in badges and filters. */
export function statusLabel(status: string): string {
  switch (status) {
    case 'PENDING':
      return 'Menunggu'
    case 'PAID':
      return 'Dibayar'
    case 'EXPIRED':
      return 'Kedaluwarsa'
    case 'SUCCESS':
      return 'Berhasil'
    case 'FAILED':
      return 'Gagal'
    case 'REQUESTED':
      return 'Diajukan'
    case 'APPROVED':
      return 'Disetujui'
    case 'REJECTED':
      return 'Ditolak'
    default:
      return status
  }
}

/** Human label for a payment method. */
export function methodLabel(method: string): string {
  switch (method) {
    case 'WALLET':
      return 'Saldo Wallet'
    case 'VA_DUMMY':
      return 'Virtual Account'
    case 'EWALLET_DUMMY':
      return 'E-Wallet'
    default:
      return method
  }
}

/** Shortens a UUID for display. Full value belongs in a title attribute. */
export function shortId(id: string): string {
  return id.length <= 8 ? id : `${id.slice(0, 8)}…`
}
