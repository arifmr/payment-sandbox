import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useAuth } from '@/store/auth'
import { useAction } from '@/hooks/useAsync'
import { validateRegisterForm, type FieldErrors, type RegisterForm } from '@/lib/validation'
import { Button } from '@/components/ui/Button'
import { TextField } from '@/components/ui/Field'
import { Alert, errorMessage } from '@/components/ui/StateView'
import './AuthPage.css'

/**
 * Merchant self-registration (SRS §2.1).
 *
 * The backend always assigns MERCHANT here — there is no role field to send, and an admin
 * can only be created by seeding. Worth stating because a role selector on a public
 * registration form is exactly how privilege escalation gets shipped.
 */
export function RegisterPage() {
  const register = useAuth((s) => s.register)
  const navigate = useNavigate()

  const [form, setForm] = useState<RegisterForm>({ name: '', email: '', password: '' })
  const [errors, setErrors] = useState<FieldErrors<RegisterForm>>({})

  const action = useAction(async (values: RegisterForm) => {
    // The store logs in straight after registering, so the user is not asked for the same
    // credentials twice.
    await register(values.name.trim(), values.email.trim(), values.password)
    navigate('/merchant', { replace: true })
  })

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    const result = validateRegisterForm(form)
    setErrors(result.errors)
    if (!result.valid) return
    void action.run(form)
  }

  function update<K extends keyof RegisterForm>(key: K, value: RegisterForm[K]) {
    setForm((f) => ({ ...f, [key]: value }))
    if (errors[key]) setErrors((e) => ({ ...e, [key]: undefined }))
    if (action.error) action.reset()
  }

  return (
    <div className="auth-page">
      <div className="auth-card">
        <header className="auth-head">
          <h1 className="auth-title">Daftar Merchant</h1>
          <p className="auth-sub muted text-sm">
            Wallet dengan saldo 0 dibuat otomatis untuk akun baru.
          </p>
        </header>

        <form className="stack" onSubmit={handleSubmit} noValidate>
          {action.error && <Alert>{errorMessage(action.error)}</Alert>}

          <TextField
            label="Nama Toko"
            name="name"
            autoComplete="organization"
            placeholder="Toko A"
            value={form.name}
            error={errors.name}
            required
            onChange={(e) => update('name', e.target.value)}
          />

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
            autoComplete="new-password"
            value={form.password}
            error={errors.password}
            hint="Minimal 8 karakter."
            required
            onChange={(e) => update('password', e.target.value)}
          />

          <Button type="submit" fullWidth loading={action.isPending}>
            Daftar
          </Button>
        </form>

        <p className="auth-foot text-sm muted">
          Sudah punya akun? <Link to="/login">Masuk</Link>
        </p>
      </div>
    </div>
  )
}
