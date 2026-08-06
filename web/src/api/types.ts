/**
 * Wire types mirroring the Go DTOs in `internal/model`.
 *
 * These are hand-written rather than generated from the OpenAPI spec. The trade-off is
 * deliberate: generation would guarantee they stay in sync, but adds a codegen step to
 * the build. Instead the surface is small and the response parsers assert the shape at
 * runtime, so a backend change surfaces as a clear error rather than `undefined`
 * spreading through the UI.
 */

// ── enums (must match internal/constant) ──────────────────────────────────────

export const ROLES = ['MERCHANT', 'ADMIN'] as const
export type Role = (typeof ROLES)[number]

export const INVOICE_STATUSES = ['PENDING', 'PAID', 'EXPIRED'] as const
export type InvoiceStatus = (typeof INVOICE_STATUSES)[number]

export const PAYMENT_STATUSES = ['PENDING', 'SUCCESS', 'FAILED'] as const
export type PaymentIntentStatus = (typeof PAYMENT_STATUSES)[number]

export const PAYMENT_METHODS = ['WALLET', 'VA_DUMMY', 'EWALLET_DUMMY'] as const
export type PaymentMethod = (typeof PAYMENT_METHODS)[number]

export const REFUND_STATUSES = ['REQUESTED', 'APPROVED', 'REJECTED', 'SUCCESS', 'FAILED'] as const
export type RefundStatus = (typeof REFUND_STATUSES)[number]

export const TOPUP_STATUSES = ['PENDING', 'SUCCESS', 'FAILED'] as const
export type TopupStatus = (typeof TOPUP_STATUSES)[number]

/** Admin actions on the single refund endpoint. */
export const REFUND_ACTIONS = ['APPROVE', 'REJECT', 'PROCESS', 'FAIL'] as const
export type RefundAction = (typeof REFUND_ACTIONS)[number]

/** Admin actions for payment intents and top-ups. */
export const SETTLE_ACTIONS = ['SUCCESS', 'FAILED'] as const
export type SettleAction = (typeof SETTLE_ACTIONS)[number]

// ── responses ─────────────────────────────────────────────────────────────────

export interface User {
  id: string
  email: string
  name: string
  role: Role
}

export interface LoginResponse {
  access_token: string
  access_expires_at: string
  refresh_token: string
  refresh_expires_at: string
  user: User
}

export interface Invoice {
  id: string
  invoice_number: string
  merchant_id: string
  customer_name: string
  customer_email: string
  description: string
  amount: number
  status: InvoiceStatus
  due_date: string
  payment_token: string
  payment_link?: string
  created_at: string
  paid_at?: string
}

/** The payment page's view of an invoice — deliberately narrower than `Invoice`. */
export interface PublicInvoice {
  invoice_number: string
  merchant_name: string
  customer_name: string
  description: string
  amount: number
  status: InvoiceStatus
  due_date: string
}

export interface PaymentIntent {
  id: string
  invoice_id: string
  method: PaymentMethod
  status: PaymentIntentStatus
  amount: number
  created_at: string
  processed_at?: string
}

export interface Refund {
  id: string
  invoice_id: string
  payment_intent_id: string
  merchant_id: string
  amount: number
  reason: string
  status: RefundStatus
  created_at: string
  processed_at?: string
}

export interface Wallet {
  merchant_id: string
  balance: number
  updated_at: string
}

export interface Topup {
  id: string
  merchant_id: string
  amount: number
  status: TopupStatus
  created_at: string
  processed_at?: string
}

export interface DashboardStats {
  total_invoices: number
  total_paid: number
  total_failed: number
  total_expired: number
  total_amount_paid: number
  total_amount_refund: number
}

// ── envelopes ─────────────────────────────────────────────────────────────────

export interface PaginationMeta {
  page: number
  page_size: number
  total: number
}

export interface Paginated<T> {
  data: T[]
  pagination: PaginationMeta
}

export interface ApiErrorBody {
  error: {
    code: string
    message: string
  }
}

// ── requests ──────────────────────────────────────────────────────────────────

export interface RegisterInput {
  name: string
  email: string
  password: string
}

export interface LoginInput {
  email: string
  password: string
}

export interface CreateInvoiceInput {
  customer_name: string
  customer_email?: string
  description?: string
  amount: number
  due_date: string
}

export interface RequestRefundInput {
  invoice_id: string
  amount: number
  reason?: string
}

// ── query params ──────────────────────────────────────────────────────────────

export interface PageQuery {
  page?: number
  page_size?: number
}

export interface InvoiceQuery extends PageQuery {
  status?: InvoiceStatus
  from?: string
  to?: string
}

export interface PaymentIntentQuery extends PageQuery {
  invoice_id?: string
  status?: PaymentIntentStatus
}

export interface DashboardQuery {
  merchant_id?: string
  from?: string
  to?: string
}

export interface TopupQuery extends PageQuery {
  merchant_id?: string
}
