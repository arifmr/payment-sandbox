import http from 'k6/http'
import { check, group, sleep } from 'k6'
import { Trend, Rate } from 'k6/metrics'

/**
 * Load test for the payment sandbox API.
 *
 * The bar comes from the spec, not from taste: SRS §5.1 requires a normal API response in
 * ≤ 300 ms. That number is the threshold below, so a run either meets the requirement or
 * exits non-zero — no reading tea leaves off a summary table.
 *
 * Deliberately NOT under load: /auth/*. Those endpoints are rate limited on purpose
 * (30/min per IP, 5/min per account — agent_documentation/04-security.md §10), so hammering
 * them measures the limiter, not the application. Worse, bcrypt at cost 12 is *designed* to
 * take 200-400 ms, so a login would blow a 300 ms threshold while behaving exactly as
 * intended. Credentials are therefore obtained once in setup() and reused, which is also
 * what a real client does.
 *
 * Every request carries a `name` tag so the summary reports each endpoint separately.
 * Without it k6 aggregates everything into one http_req_duration and a slow admin
 * aggregation would hide behind a fast wallet read.
 */

const BASE = __ENV.BASE_URL || 'http://localhost:8080'
const API = `${BASE}/api/v1`

// SRS §5.1. Overridable so a slower machine can still get a signal instead of a wall of red.
const SLO_MS = Number(__ENV.SLO_MS || 300)

// Per-endpoint latency, so a regression can be attributed rather than merely noticed.
const dInvoiceList = new Trend('dur_invoice_list', true)
const dInvoiceDetail = new Trend('dur_invoice_detail', true)
const dWallet = new Trend('dur_wallet', true)
const dRefundList = new Trend('dur_refund_list', true)
const dPublicPay = new Trend('dur_public_pay', true)
const dDashboard = new Trend('dur_admin_dashboard', true)
const dInvoiceCreate = new Trend('dur_invoice_create', true)

const sloOk = new Rate('within_slo')

export const options = {
  // p(90) is what was asked for; the rest are here because a p(90) that passes while p(99)
  // is seconds away is a distribution worth seeing, not hiding.
  summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],

  scenarios: {
    load: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: __ENV.RAMP || '10s', target: Number(__ENV.VUS || 10) },
        { duration: __ENV.DURATION || '30s', target: Number(__ENV.VUS || 10) },
        { duration: '5s', target: 0 },
      ],
      gracefulRampDown: '5s',
    },
  },

  thresholds: {
    // The headline requirement — scoped to phase:load on purpose.
    //
    // setup() logs in twice, and bcrypt at cost 12 is deliberately 200-400 ms
    // (agent_documentation/04-security.md §1). Those few requests are counted in the
    // unfiltered http_req_duration and drag its tail into the hundreds of milliseconds,
    // which looks like a latency problem and is actually the password hash doing its job.
    // Filtering by phase measures the traffic under test rather than the fixture setup.
    'http_req_duration{phase:load}': [`p(90)<${SLO_MS}`],
    // A fast run that is failing requests is not a passing run.
    http_req_failed: ['rate<0.01'],
    checks: ['rate>0.99'],

    // Per-endpoint, so one slow surface cannot be averaged away by fast ones.
    // No `{}` suffix: an empty tag selector is a parse error in k6, and a bare metric
    // name is what applies the threshold to the whole metric anyway.
    dur_invoice_list: [`p(90)<${SLO_MS}`],
    dur_invoice_detail: [`p(90)<${SLO_MS}`],
    dur_wallet: [`p(90)<${SLO_MS}`],
    dur_refund_list: [`p(90)<${SLO_MS}`],
    dur_public_pay: [`p(90)<${SLO_MS}`],
    // The dashboard runs three aggregate queries with FILTER and a JOIN, so it is the one
    // most likely to break first. Same bar — it is a normal API response per §5.1.
    dur_admin_dashboard: [`p(90)<${SLO_MS}`],
    // Writes go through a transaction, so they get their own (looser) budget.
    dur_invoice_create: [`p(90)<${SLO_MS * 2}`],
  },
}

/**
 * Request params. `phase` separates fixture setup from the traffic under test so the
 * headline threshold can exclude bcrypt logins; `name` groups a metric per endpoint so
 * one slow surface is visible instead of averaged into the rest.
 */
function params(token, { phase = 'load', name } = {}) {
  return {
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    tags: { phase, ...(name ? { name } : {}) },
  }
}

const setupParams = (token) => params(token, { phase: 'setup' })

/**
 * Runs once. Creates the fixtures the load phase reads, and obtains tokens.
 *
 * Failing loudly here matters: a load test that silently runs against 401s reports
 * beautiful latency for an API that did no work.
 */
export function setup() {
  const ready = http.get(`${BASE}/readyz`, setupParams())
  if (ready.status !== 200) {
    throw new Error(
      `${BASE}/readyz returned ${ready.status}. Start the stack first: docker compose up -d`,
    )
  }

  const email = `k6-${Date.now()}@example.com`
  const password = 'password123'

  const reg = http.post(
    `${API}/auth/register`,
    JSON.stringify({ name: 'k6 Merchant', email, password }),
    setupParams(),
  )
  if (reg.status !== 201) throw new Error(`register failed: ${reg.status} ${reg.body}`)

  const login = http.post(
    `${API}/auth/login`,
    JSON.stringify({ email, password }),
    setupParams(),
  )
  if (login.status !== 200) throw new Error(`login failed: ${login.status} ${login.body}`)
  const merchantToken = login.json('access_token')

  const adminLogin = http.post(
    `${API}/auth/login`,
    JSON.stringify({
      email: __ENV.ADMIN_EMAIL || 'admin@example.com',
      password: __ENV.ADMIN_PASSWORD || 'admin12345',
    }),
    setupParams(),
  )
  if (adminLogin.status !== 200) {
    throw new Error(
      `admin login failed: ${adminLogin.status}. The admin is seeded by \`go run ./cmd/api migrate\`.`,
    )
  }
  const adminToken = adminLogin.json('access_token')

  // A handful of invoices so list endpoints have something to page through, and one PAID
  // invoice so the public payment page is reading a realistic record.
  const invoiceIds = []
  let payToken = ''
  for (let i = 0; i < 25; i++) {
    const res = http.post(
      `${API}/invoices`,
      JSON.stringify({
        customer_name: `Pelanggan ${i}`,
        amount: 10000 + i * 100,
        due_date: '2030-01-01T00:00:00Z',
        description: 'k6 fixture',
      }),
      setupParams(merchantToken),
    )
    if (res.status !== 201) throw new Error(`fixture invoice failed: ${res.status} ${res.body}`)
    invoiceIds.push(res.json('id'))
    if (i === 0) payToken = res.json('payment_token')
  }

  return { merchantToken, adminToken, payToken, invoiceIds }
}

export default function (data) {
  group('merchant reads', () => {
    const list = http.get(
      `${API}/invoices?page=1&page_size=20`,
      params(data.merchantToken, { name: 'GET /invoices' }),
    )
    record(dInvoiceList, list, 200)

    // A random invoice each iteration, so the run measures the query rather than a row
    // that Postgres has had every chance to keep hot in cache.
    const id = data.invoiceIds[Math.floor(Math.random() * data.invoiceIds.length)]
    const detail = http.get(
      `${API}/invoices/${id}`,
      params(data.merchantToken, { name: 'GET /invoices/:id' }),
    )
    record(dInvoiceDetail, detail, 200)

    const w = http.get(`${API}/wallet`, params(data.merchantToken, { name: 'GET /wallet' }))
    record(dWallet, w, 200)

    const r = http.get(
      `${API}/refunds?page=1&page_size=20`,
      params(data.merchantToken, { name: 'GET /refunds' }),
    )
    record(dRefundList, r, 200)
  })

  group('public payment page', () => {
    // No auth: this is the surface a customer hits, and the one most exposed to traffic
    // spikes because a payment link can be shared anywhere.
    const p = http.get(`${API}/pay/${data.payToken}`, params(null, { name: 'GET /pay/:token' }))
    record(dPublicPay, p, 200)
  })

  group('admin', () => {
    const d = http.get(
      `${API}/admin/dashboard`,
      params(data.adminToken, { name: 'GET /admin/dashboard' }),
    )
    record(dDashboard, d, 200)
  })

  // Writes are a minority of real traffic, so they are a minority here. Running them on
  // every iteration would both misrepresent the mix and grow the table unboundedly during
  // a long soak.
  if (Math.random() < 0.2) {
    group('merchant write', () => {
      const c = http.post(
        `${API}/invoices`,
        JSON.stringify({
          customer_name: 'Pelanggan k6',
          amount: 25000,
          due_date: '2030-01-01T00:00:00Z',
          description: 'k6 load',
        }),
        params(data.merchantToken, { name: 'POST /invoices' }),
      )
      record(dInvoiceCreate, c, 201)
    })
  }

  // Think time. Without it VUs behave like a benchmark hammer rather than users, and the
  // numbers describe a queue depth nobody will ever see.
  sleep(Number(__ENV.SLEEP || 1))
}

/** Records duration, asserts the status, and tracks the SLO as its own pass rate. */
function record(trend, res, expectedStatus) {
  trend.add(res.timings.duration)
  sloOk.add(res.timings.duration < SLO_MS)
  check(res, {
    [`status is ${expectedStatus}`]: (r) => r.status === expectedStatus,
  })
}
