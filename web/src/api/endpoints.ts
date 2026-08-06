import { api } from './client'
import type {
  CreateInvoiceInput,
  DashboardQuery,
  DashboardStats,
  Invoice,
  InvoiceQuery,
  LoginInput,
  LoginResponse,
  Paginated,
  PaymentIntent,
  PaymentIntentQuery,
  PaymentMethod,
  PublicInvoice,
  Refund,
  RefundAction,
  RegisterInput,
  RequestRefundInput,
  SettleAction,
  Topup,
  TopupQuery,
  User,
  Wallet,
  PageQuery,
} from './types'

/**
 * One typed function per endpoint. Pages call these instead of touching the client, so
 * the URL surface lives in exactly one file and a backend path change is a single edit.
 *
 * Read endpoints take an optional AbortSignal. Without it, `useAsync` could not cancel a
 * request when the component unmounts or the filters change, and a superseded response
 * could still land — the race that shows stale data after a fast navigation.
 */

/** Builds the client options for a read, including the signal only when supplied. */
function read(query: Record<string, unknown>, signal?: AbortSignal) {
  return {
    query: query as Record<string, string | number | undefined | null>,
    ...(signal ? { signal } : {}),
  }
}

// ── auth (public) ─────────────────────────────────────────────────────────────

export const auth = {
  register: (input: RegisterInput) =>
    api.post<User>('/auth/register', input, { public: true }),

  login: (input: LoginInput) =>
    api.post<LoginResponse>('/auth/login', input, { public: true }),

  /**
   * Not called directly by the UI — the client handles rotation on 401 with
   * single-flight protection. Exposed only for tests.
   */
  refresh: (refreshToken: string) =>
    api.post<LoginResponse>('/auth/refresh', { refresh_token: refreshToken }, { public: true }),

  logout: (refreshToken: string) =>
    api.post<void>('/auth/logout', { refresh_token: refreshToken }, { public: true }),
}

// ── merchant: wallet (SRS §2.2) ───────────────────────────────────────────────

export const wallet = {
  get: (signal?: AbortSignal) =>
    api.get<Wallet>('/wallet', signal ? { signal } : {}),

  requestTopup: (amount: number) => api.post<Topup>('/wallet/topup', { amount }),

  listMine: (query: PageQuery = {}, signal?: AbortSignal) =>
    api.get<Paginated<Topup>>('/wallet/topups', read({ ...query }, signal)),
}

// ── merchant: invoices (SRS §2.3) ─────────────────────────────────────────────

export const invoices = {
  create: (input: CreateInvoiceInput) => api.post<Invoice>('/invoices', input),

  list: (query: InvoiceQuery = {}, signal?: AbortSignal) =>
    api.get<Paginated<Invoice>>('/invoices', read({ ...query }, signal)),

  get: (id: string, signal?: AbortSignal) =>
    api.get<Invoice>(`/invoices/${id}`, signal ? { signal } : {}),
}

// ── public payment page (SRS §2.4 / §4.3) ─────────────────────────────────────

export const pay = {
  /** Auth is optional here: an anonymous payer is allowed. */
  getInvoice: (token: string, signal?: AbortSignal) =>
    api.get<PublicInvoice>(`/pay/${encodeURIComponent(token)}`, {
      public: true,
      ...(signal ? { signal } : {}),
    }),

  /**
   * Creates the intent. Sent with credentials when the payer happens to be logged in, so
   * a WALLET payment can debit them — the backend attaches the payer from the token.
   */
  createIntent: (token: string, method: PaymentMethod) =>
    api.post<PaymentIntent>(`/pay/${encodeURIComponent(token)}`, { method }),

  /** Lets the payer poll their own intent. Authorised by possession of the token. */
  getIntent: (token: string, intentId: string) =>
    api.get<PaymentIntent>(
      `/pay/${encodeURIComponent(token)}/intents/${encodeURIComponent(intentId)}`,
      { public: true },
    ),
}

// ── merchant: refunds (SRS §2.5) ──────────────────────────────────────────────

export const refunds = {
  request: (input: RequestRefundInput) => api.post<Refund>('/refunds', input),

  listMine: (query: PageQuery = {}, signal?: AbortSignal) =>
    api.get<Paginated<Refund>>('/refunds', read({ ...query }, signal)),
}

// ── admin (SRS §2.4 / §2.5 / §2.6) ────────────────────────────────────────────

export const admin = {
  dashboard: (query: DashboardQuery = {}, signal?: AbortSignal) =>
    api.get<DashboardStats>('/admin/dashboard', read({ ...query }, signal)),

  listPayments: (query: PaymentIntentQuery = {}, signal?: AbortSignal) =>
    api.get<Paginated<PaymentIntent>>('/admin/payments', read({ ...query }, signal)),

  getPayment: (id: string) => api.get<PaymentIntent>(`/admin/payments/${id}`),

  settlePayment: (id: string, action: SettleAction) =>
    api.patch<PaymentIntent>(`/admin/payments/${id}`, { action }),

  listRefunds: (query: PageQuery = {}, signal?: AbortSignal) =>
    api.get<Paginated<Refund>>('/admin/refunds', read({ ...query }, signal)),

  actOnRefund: (id: string, action: RefundAction) =>
    api.patch<Refund>(`/admin/refunds/${id}`, { action }),

  listTopups: (query: TopupQuery = {}, signal?: AbortSignal) =>
    api.get<Paginated<Topup>>('/admin/topups', read({ ...query }, signal)),

  settleTopup: (id: string, action: SettleAction) =>
    api.patch<Topup>(`/admin/topups/${id}`, { action }),
}
