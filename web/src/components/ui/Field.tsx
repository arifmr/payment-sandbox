import type { InputHTMLAttributes, ReactNode, SelectHTMLAttributes, TextareaHTMLAttributes } from 'react'
import { useId } from 'react'
import './Field.css'

interface BaseFieldProps {
  label: string
  /** Validation message. Its presence is what marks the field invalid. */
  error?: string | undefined
  hint?: ReactNode
  required?: boolean
}

/**
 * Wraps a control with its label, hint and error.
 *
 * The accessibility wiring is the reason this exists rather than repeating markup:
 *  - `htmlFor`/`id` are generated with useId, so clicking the label focuses the control
 *  - `aria-invalid` marks the field for screen readers, not just visually
 *  - `aria-describedby` points at the error text, so it is announced on focus
 *  - the error carries role="alert", so a message appearing after submit is announced
 *
 * Getting this wrong is the usual way a form is unusable without a mouse, and it is
 * invisible in a screenshot.
 */
function useFieldIds(error?: string) {
  const id = useId()
  return {
    controlId: `field-${id}`,
    errorId: error ? `field-${id}-error` : undefined,
    hintId: `field-${id}-hint`,
  }
}

function Shell({
  label,
  error,
  hint,
  required,
  controlId,
  errorId,
  hintId,
  children,
}: BaseFieldProps & {
  controlId: string
  errorId: string | undefined
  hintId: string
  children: ReactNode
}) {
  return (
    <div className={`field${error ? ' field-invalid' : ''}`}>
      <label className="field-label" htmlFor={controlId}>
        {label}
        {required && (
          <span className="field-required" aria-hidden="true">
            *
          </span>
        )}
      </label>
      {children}
      {hint && !error && (
        <p className="field-hint" id={hintId}>
          {hint}
        </p>
      )}
      {error && (
        <p className="field-error" id={errorId} role="alert">
          {error}
        </p>
      )}
    </div>
  )
}

type TextFieldProps = BaseFieldProps &
  Omit<InputHTMLAttributes<HTMLInputElement>, 'className' | 'id'>

export function TextField({ label, error, hint, required, ...rest }: TextFieldProps) {
  const { controlId, errorId, hintId } = useFieldIds(error)
  return (
    <Shell
      label={label}
      error={error}
      hint={hint}
      required={required}
      controlId={controlId}
      errorId={errorId}
      hintId={hintId}
    >
      <input
        id={controlId}
        className="field-control"
        aria-invalid={error ? true : undefined}
        aria-describedby={errorId ?? (hint ? hintId : undefined)}
        required={required}
        {...rest}
      />
    </Shell>
  )
}

type SelectFieldProps = BaseFieldProps &
  Omit<SelectHTMLAttributes<HTMLSelectElement>, 'className' | 'id'> & {
    children: ReactNode
  }

export function SelectField({
  label,
  error,
  hint,
  required,
  children,
  ...rest
}: SelectFieldProps) {
  const { controlId, errorId, hintId } = useFieldIds(error)
  return (
    <Shell
      label={label}
      error={error}
      hint={hint}
      required={required}
      controlId={controlId}
      errorId={errorId}
      hintId={hintId}
    >
      <select
        id={controlId}
        className="field-control"
        aria-invalid={error ? true : undefined}
        aria-describedby={errorId ?? (hint ? hintId : undefined)}
        {...rest}
      >
        {children}
      </select>
    </Shell>
  )
}

type TextAreaFieldProps = BaseFieldProps &
  Omit<TextareaHTMLAttributes<HTMLTextAreaElement>, 'className' | 'id'>

export function TextAreaField({ label, error, hint, required, ...rest }: TextAreaFieldProps) {
  const { controlId, errorId, hintId } = useFieldIds(error)
  return (
    <Shell
      label={label}
      error={error}
      hint={hint}
      required={required}
      controlId={controlId}
      errorId={errorId}
      hintId={hintId}
    >
      <textarea
        id={controlId}
        className="field-control field-textarea"
        aria-invalid={error ? true : undefined}
        aria-describedby={errorId ?? (hint ? hintId : undefined)}
        rows={3}
        {...rest}
      />
    </Shell>
  )
}
