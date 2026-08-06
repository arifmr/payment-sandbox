import { useState } from 'react'
import { Link, useLocation, useNavigate } from 'react-router-dom'
import { homePathFor, useAuth } from '@/store/auth'
import { useAction } from '@/hooks/useAsync'
import { validateLoginForm, type FieldErrors, type LoginForm } from '@/lib/validation'
import { Button } from '@/components/ui/Button'
import { TextField } from '@/components/ui/Field'
import { Alert, errorMessage } from '@/components/ui/StateView'
import { ApiError } from '@/api/client'
import './AuthPage.css'

export function LoginPage() {
  const login = useAuth((s) => s.login)
  const navigate = useNavigate()
  const location = useLocation()

  const [form, setForm] = useState<LoginForm>({ email: '', password: '' })
  const [errors, setErrors] = useState<FieldErrors<LoginForm>>({})

  const action = useAction(async (values: LoginForm) => {
    const user = await login(values.email.trim(), values.password)
    // Return to the page they originally asked for, if the guard recorded one.
    const from = (location.state as { from?: string } | null)?.from
    navigate(from ?? homePathFor(user.role), { replace: true })
    return user
  })

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    const result = validateLoginForm(form)
    setErrors(result.errors)
    if (!result.valid) return
    void action.run(form)
  }

  /** Clears a field's error as soon as it is edited, so it stops nagging mid-correction. */
  function update<K extends keyof LoginForm>(key: K, value: LoginForm[K]) {
    setForm((f) => ({ ...f, [key]: value }))
    if (errors[key]) setErrors((e) => ({ ...e, [key]: undefined }))
    if (action.error) action.reset()
  }

  const rateLimited = action.error instanceof ApiError && action.error.code === 'RATE_LIMITED'

  return (
    <div className="auth-page">
      <div className="auth-card">
        <header className="auth-head">
          <h1 className="auth-title">Masuk</h1>
          <p className="auth-sub muted text-sm">
            Payment Sandbox — simulasi pembayaran tanpa dana nyata.
          </p>
        </header>

        {/* noValidate turns off the browser's own bubbles so our messages are the only
            ones shown, and stay consistent with the backend's rules. */}
        <form className="stack" onSubmit={handleSubmit} noValidate>
          {action.error && (
            <Alert tone={rateLimited ? 'warning' : 'danger'}>
              {rateLimited
                ? 'Terlalu banyak percobaan masuk. Tunggu sebentar lalu coba lagi.'
                : errorMessage(action.error)}
            </Alert>
          )}

          <TextField
            label="Email"
            type="email"
            name="email"
            autoComplete="email"
            placeholder="toko@example.com"
            value={form.email}
            error={errors.email}
            required
            onChange={(e) => update('email', e.target.value)}
          />

          <TextField
            label="Password"
            type="password"
            name="password"
            autoComplete="current-password"
            value={form.password}
            error={errors.password}
            required
            onChange={(e) => update('password', e.target.value)}
          />

          <Button type="submit" fullWidth loading={action.isPending}>
            Masuk
          </Button>
        </form>

        <p className="auth-foot text-sm muted">
          Belum punya akun merchant? <Link to="/register">Daftar di sini</Link>
        </p>
      </div>
    </div>
  )
}
