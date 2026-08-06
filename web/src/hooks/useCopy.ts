import { useCallback, useEffect, useRef, useState } from 'react'

/**
 * Copy-to-clipboard with transient confirmation, for the payment-link copy in SRS §4.2.
 *
 * Includes a fallback because `navigator.clipboard` is unavailable in two situations that
 * are easy to hit: any non-HTTPS origin other than localhost, and older browsers. Without
 * the fallback, copy silently does nothing on a staging box served over plain HTTP.
 */
export function useCopy(resetAfterMs = 2000) {
  const [copied, setCopied] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current)
    },
    [],
  )

  const copy = useCallback(
    async (text: string) => {
      setError(null)
      try {
        if (navigator.clipboard?.writeText) {
          await navigator.clipboard.writeText(text)
        } else if (!legacyCopy(text)) {
          throw new Error('clipboard unavailable')
        }
        setCopied(true)
        if (timer.current) clearTimeout(timer.current)
        timer.current = setTimeout(() => setCopied(false), resetAfterMs)
      } catch {
        // Tell the user to copy manually rather than leaving a button that does nothing.
        setError('Gagal menyalin. Silakan salin manual.')
      }
    },
    [resetAfterMs],
  )

  return { copy, copied, error }
}

/** execCommand fallback for insecure origins. Deprecated, but still the only option there. */
function legacyCopy(text: string): boolean {
  try {
    const el = document.createElement('textarea')
    el.value = text
    // Keep it out of view and off the tab order so it never flashes or steals focus.
    el.setAttribute('readonly', '')
    el.style.position = 'fixed'
    el.style.opacity = '0'
    el.style.pointerEvents = 'none'
    document.body.appendChild(el)
    el.select()
    const ok = document.execCommand('copy')
    document.body.removeChild(el)
    return ok
  } catch {
    return false
  }
}
