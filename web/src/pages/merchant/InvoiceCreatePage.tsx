import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { invoices } from '@/api/endpoints'
import { useAction } from '@/hooks/useAsync'
import { dateInputToRFC3339, todayISODate } from '@/lib/format'
import { validateInvoiceForm, type FieldErrors, type InvoiceForm } from '@/lib/validation'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import { TextAreaField, TextField } from '@/components/ui/Field'
import { Alert, errorMessage } from '@/components/ui/StateView'
import { PageHeader } from '@/components/layout/PageHeader'

/**
 * Create invoice form (SRS §4.2, validation per §4.5).
 *
 * Validation runs on submit rather than on every keystroke: complaining about an
 * incomplete email while it is still being typed is noise. Once a field has an error, it
 * clears as soon as the user edits it, so the correction is acknowledged immediately.
 */
export function InvoiceCreatePage() {
  const navigate = useNavigate()
  const today = todayISODate()

  const [form, setForm] = useState<InvoiceForm>({
    customer_name: '',
    customer_email: '',
    description: '',
    amount: '',
    // Default to a week out: a sensible starting point that is already valid.
    due_date: defaultDueDate(),
  })
  const [errors, setErrors] = useState<FieldErrors<InvoiceForm>>({})

  const action = useAction(async (values: InvoiceForm) => {
    const created = await invoices.create({
      customer_name: values.customer_name.trim(),
      ...(values.customer_email.trim() ? { customer_email: values.customer_email.trim() } : {}),
      ...(values.description.trim() ? { description: values.description.trim() } : {}),
      amount: Number(values.amount),
      due_date: dateInputToRFC3339(values.due_date),
    })
    // Straight to the detail page, which is where the payment link lives — the thing the
    // merchant actually wanted from creating an invoice.
    navigate(`/merchant/invoices/${created.id}`, { replace: true })
    return created
  })

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    const result = validateInvoiceForm(form, today)
    setErrors(result.errors)
    if (!result.valid) return
    void action.run(form)
  }

  function update<K extends keyof InvoiceForm>(key: K, value: InvoiceForm[K]) {
    setForm((f) => ({ ...f, [key]: value }))
    if (errors[key]) setErrors((e) => ({ ...e, [key]: undefined }))
    if (action.error) action.reset()
  }

  return (
    <div className="stack">
      <PageHeader
        title="Buat Invoice"
        description="Nomor invoice dan tautan pembayaran dibuat otomatis oleh sistem."
      />

      <Card>
        <form className="stack" onSubmit={handleSubmit} noValidate>
          {action.error && <Alert>{errorMessage(action.error)}</Alert>}

          <TextField
            label="Nama Pelanggan"
            value={form.customer_name}
            error={errors.customer_name}
            required
            maxLength={255}
            placeholder="Budi Santoso"
            onChange={(e) => update('customer_name', e.target.value)}
          />

          <TextField
            label="Email Pelanggan"
            type="email"
            value={form.customer_email}
            error={errors.customer_email}
            hint="Opsional."
            placeholder="budi@example.com"
            onChange={(e) => update('customer_email', e.target.value)}
          />

          <TextField
            label="Nominal (IDR)"
            type="number"
            inputMode="numeric"
            // min/step are hints for the browser's own UI; validation.ts is what actually
            // decides, since these attributes can be bypassed.
            min={1}
            step={1}
            value={form.amount}
            error={errors.amount}
            required
            hint="Bilangan bulat, lebih besar dari 0."
            placeholder="50000"
            onChange={(e) => update('amount', e.target.value)}
          />

          <TextField
            label="Jatuh Tempo"
            type="date"
            // Blocks past dates in the picker itself (SRS §4.5); validated again on submit.
            min={today}
            value={form.due_date}
            error={errors.due_date}
            required
            onChange={(e) => update('due_date', e.target.value)}
          />

          <TextAreaField
            label="Deskripsi"
            value={form.description}
            error={errors.description}
            hint="Opsional, maksimal 500 karakter."
            maxLength={500}
            placeholder="Pembayaran pesanan #1"
            onChange={(e) => update('description', e.target.value)}
          />

          <div className="row">
            <Button type="submit" loading={action.isPending}>
              Buat Invoice
            </Button>
            <Button
              variant="secondary"
              onClick={() => navigate('/merchant/invoices')}
              disabled={action.isPending}
            >
              Batal
            </Button>
          </div>
        </form>
      </Card>
    </div>
  )
}

/** Seven days from today, in the user's own calendar. */
function defaultDueDate(): string {
  const d = new Date()
  d.setDate(d.getDate() + 7)
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}
