import { useCopy } from '@/hooks/useCopy'
import { Button } from './Button'
import './CopyField.css'

interface CopyFieldProps {
  label: string
  value: string
  /** Absolute URL to open in a new tab, when the value is a link. */
  href?: string
}

/**
 * Read-only value with a copy button — the payment link preview from SRS §4.2.
 *
 * The input is readOnly rather than disabled: a disabled input cannot be focused or
 * selected, so a user whose clipboard access is blocked would have no way to copy the
 * link by hand.
 */
export function CopyField({ label, value, href }: CopyFieldProps) {
  const { copy, copied, error } = useCopy()

  return (
    <div className="copy-field">
      <label className="copy-label" htmlFor={`copy-${label}`}>
        {label}
      </label>
      <div className="copy-row">
        <input
          id={`copy-${label}`}
          className="copy-input mono"
          value={value}
          readOnly
          onFocus={(e) => e.currentTarget.select()}
        />
        <Button variant="secondary" size="sm" onClick={() => void copy(value)}>
          {copied ? 'Tersalin ✓' : 'Salin'}
        </Button>
        {href && (
          <Button variant="ghost" size="sm" onClick={() => window.open(href, '_blank', 'noopener')}>
            Buka
          </Button>
        )}
      </div>
      {/* aria-live so the confirmation is announced, not just shown. */}
      <span className="sr-only" aria-live="polite">
        {copied ? 'Tautan tersalin ke clipboard' : ''}
      </span>
      {error && <p className="copy-error text-xs">{error}</p>}
    </div>
  )
}
