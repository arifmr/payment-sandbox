/**
 * Remembers, per payment token, the intent a payer is currently waiting on.
 *
 * Without this, PaymentPage's intent lives only in React state: refreshing the tab (or a
 * payer closing and reopening the link while an admin has not settled it yet) loses it, and
 * the page falls back to showing the method picker — inviting a second payment attempt for
 * something already in flight. The backend is not at fault here; the intent is still
 * PENDING in the database, the frontend has simply forgotten its id.
 *
 * Scoped by token (not a single value) because a browser can hold more than one payment
 * link open — in different tabs, or one after another — and restoring the wrong intent
 * would show a stranger's payment status.
 */

const STORAGE_KEY = 'payment-sandbox.pending-intents'

type PendingIntents = Record<string, string>

function readAll(): PendingIntents {
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY)
    if (!raw) return {}
    const parsed: unknown = JSON.parse(raw)
    return parsed && typeof parsed === 'object' ? (parsed as PendingIntents) : {}
  } catch {
    // Corrupt JSON or storage unavailable (private browsing, quota, disabled). Losing the
    // ability to resume after a refresh is a minor regression, not a reason to break the
    // payment page — treat it as if nothing were stored.
    return {}
  }
}

function writeAll(all: PendingIntents): void {
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify(all))
  } catch {
    // Same reasoning as readAll: a write that fails silently is preferable to a payment
    // page that throws because storage is full or disabled.
  }
}

export function getPendingIntent(token: string): string | null {
  return readAll()[token] ?? null
}

export function setPendingIntent(token: string, intentId: string): void {
  writeAll({ ...readAll(), [token]: intentId })
}

export function clearPendingIntent(token: string): void {
  const all = readAll()
  if (!(token in all)) return
  delete all[token]
  writeAll(all)
}
