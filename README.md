# Payment Sandbox API

Implementasi backend untuk *Payment Sandbox App* (lihat `soal.txt`) — Golang + Gin + PostgreSQL + GORM, mengikuti clean architecture (handler → service → repository).

## Stack

- Go 1.22+
- Gin (HTTP)
- `database/sql` + `jackc/pgx/v5` driver
- PostgreSQL
- JWT (HS256) + bcrypt
- `log/slog` untuk structured logging
- `go-playground/validator` (terintegrasi Gin binding)

## Design Patterns

- **Repository** untuk abstraksi data access
- **Unit of Work** (`internal/transaction`) untuk operasi atomic (Payment SUCCESS, Refund SUCCESS, Topup SUCCESS)
- **State Machine** via CAS-style `UPDATE ... WHERE status = old_status` (transition invalid → `INVALID_STATE`)
- **Strategy + Factory** untuk metode pembayaran (WALLET / VA_DUMMY / EWALLET_DUMMY)
- **Optimistic Locking** pada wallet (`version` column) + guard `balance + delta >= 0` untuk debit
- **Middleware Chain**: request-id → recovery → logger → error mapper → auth → role
- **DTO + Mapper** untuk pisahkan wire format dari entity domain

## Folder Structure

```
cmd/api/                # entrypoint, DI wiring, graceful shutdown, expiry sweeper
internal/
  config/               # env loader
  domain/               # entitas: User, Wallet, Topup, Invoice, PaymentIntent, Refund
  dto/                  # request/response + mappers
  handler/              # gin handlers + router
  service/              # business logic + payment strategies
  repository/           # interfaces + GORM impl + dashboard aggregator
  middleware/           # auth, role, request-id, logger, error mapper, recovery
  transaction/          # UnitOfWork (gorm-backed)
  pkg/                  # apperror, jwt, hash, token, statemachine, pagination, logger
```

## Cara Menjalankan

### 1. Siapkan Postgres

```bash
docker compose up -d postgres
```

### 2. Konfigurasi env

```bash
cp .env.example .env
# minimal yang harus diisi: JWT_SECRET (string panjang & acak)
# opsional: ADMIN_EMAIL / ADMIN_PASSWORD utk seed admin pertama kali
```

Load env, contoh:

```bash
set -a; source .env; set +a
```

### 3. Migrate DB

```bash
go run ./cmd/api migrate
```

App akan menjalankan migrasi SQL dari `internal/repository/migrations/`

### 4. Run Service

```bash
go run ./cmd/api
```

Untuk Health check:

```bash
curl http://localhost:8080/healthz
```

## Swagger / OpenAPI

Generated docs ada di `docs/` (committed). Setelah app running, buka:

```
http://localhost:8080/swagger/index.html
```

### Re-generate (setelah ubah anotasi)

```bash
# Install swag CLI sekali
go install github.com/swaggo/swag/cmd/swag@latest

# Re-generate (jalankan dari root)
swag init -g cmd/api/main.go -o docs --parseInternal
```

## Authentication

Login mengembalikan **access token** (JWT, default 15 menit) dan **refresh token** (opaque, default 7 hari, di-hash di DB).

Setelah access token expired, panggil `POST /auth/refresh` dengan `refresh_token` — server akan **revoke** token lama dan keluarkan **pasangan baru** (rotation). Logout meng-revoke refresh token.

```bash
# Login
curl -s -X POST localhost:8080/api/v1/auth/login \
  -H 'content-type: application/json' \
  -d '{"email":"toko@example.com","password":"password123"}'
# → { "access_token": "...", "refresh_token": "...", "access_expires_at": "...", ... }

# Refresh (rotation)
curl -s -X POST localhost:8080/api/v1/auth/refresh \
  -H 'content-type: application/json' \
  -d '{"refresh_token":"..."}'

# Logout
curl -X POST localhost:8080/api/v1/auth/logout \
  -H 'content-type: application/json' \
  -d '{"refresh_token":"..."}'
```

## Endpoint Utama

| Method | Path | Role | Keterangan |
|---|---|---|---|
| POST | `/api/v1/auth/register` | public | daftar merchant |
| POST | `/api/v1/auth/login` | public | login → access + refresh token |
| POST | `/api/v1/auth/refresh` | public | rotate refresh token, dapat pasangan baru |
| POST | `/api/v1/auth/logout` | public | revoke refresh token |
| GET | `/api/v1/wallet` | merchant | lihat saldo |
| POST | `/api/v1/wallet/topup` | merchant | request top-up (PENDING) |
| GET | `/api/v1/wallet/topups` | merchant | list top-up sendiri |
| POST | `/api/v1/invoices` | merchant | buat invoice |
| GET | `/api/v1/invoices` | merchant | list invoice (filter `status`, `from`, `to`, pagination) |
| GET | `/api/v1/invoices/:id` | merchant | detail |
| GET | `/api/v1/pay/:token` | public | data invoice utk halaman pembayaran |
| POST | `/api/v1/pay/:token` | public/optional auth | buat payment intent (`{"method":"WALLET\|VA_DUMMY\|EWALLET_DUMMY"}`) |
| POST | `/api/v1/refunds` | merchant | request refund |
| GET | `/api/v1/refunds` | merchant | list refund sendiri |
| PATCH | `/api/v1/admin/topups/:id` | admin | `{"action":"SUCCESS\|FAILED"}` |
| PATCH | `/api/v1/admin/payments/:id` | admin | `{"action":"SUCCESS\|FAILED"}` |
| GET | `/api/v1/admin/refunds` | admin | list semua refund |
| PATCH | `/api/v1/admin/refunds/:id` | admin | **single endpoint** — `{"action":"APPROVE\|REJECT\|PROCESS\|FAIL"}` |
| GET | `/api/v1/admin/dashboard` | admin | statistik (filter `merchant_id`, `from`, `to`) |
| GET | `/api/v1/admin/topups` | admin | list semua top-up |
| GET | `/healthz` | public | health check |
| GET | `/swagger/*` | public | Swagger UI |

### Pagination

Query string `?page=1&page_size=20` (default 1/20, max 100). Response:
```json
{ "data": [...], "pagination": { "page": 1, "page_size": 20, "total": 42 } }
```

### Error Format

```json
{ "error": { "code": "INVALID_STATE", "message": "invalid state transition" } }
```

## State Machines

| Entity | Allowed Transitions |
|---|---|
| Invoice | PENDING → PAID / EXPIRED |
| PaymentIntent | PENDING → SUCCESS / FAILED |
| Refund | REQUESTED → APPROVED / REJECTED → SUCCESS / FAILED |
| Topup | PENDING → SUCCESS / FAILED |

Transisi invalid → HTTP 422 `INVALID_STATE`.

## Operasi Atomic

Semua operasi berikut di-wrap dalam satu DB transaction (UnitOfWork):

- **Payment SUCCESS**: update intent → update invoice → wallet credit/debit (sesuai metode)
- **Refund PROCESS (SUCCESS)**: update refund + debit wallet merchant
- **Topup SUCCESS**: update topup + credit wallet merchant
- **Register merchant**: create user + create wallet

## Quick End-to-End

```bash
# 1. Register merchant
curl -X POST localhost:8080/api/v1/auth/register \
  -H 'content-type: application/json' \
  -d '{"name":"Toko A","email":"toko@example.com","password":"password123"}'

# 2. Login → simpan token
TOKEN=$(curl -s -X POST localhost:8080/api/v1/auth/login \
  -H 'content-type: application/json' \
  -d '{"email":"toko@example.com","password":"password123"}' | jq -r .access_token)

# 3. Buat invoice
INV=$(curl -s -X POST localhost:8080/api/v1/invoices \
  -H "authorization: Bearer $TOKEN" -H 'content-type: application/json' \
  -d '{"customer_name":"Budi","amount":50000,"due_date":"2030-01-01T00:00:00Z","description":"Order #1"}')
TOKEN_PAY=$(echo $INV | jq -r .payment_token)

# 4. Public payer buat intent
INTENT=$(curl -s -X POST localhost:8080/api/v1/pay/$TOKEN_PAY \
  -H 'content-type: application/json' \
  -d '{"method":"VA_DUMMY"}')
INTENT_ID=$(echo $INTENT | jq -r .id)

# 5. Admin approve payment
ADMIN_TOKEN=$(curl -s -X POST localhost:8080/api/v1/auth/login \
  -H 'content-type: application/json' \
  -d '{"email":"admin@example.com","password":"admin12345"}' | jq -r .access_token)

curl -X PATCH localhost:8080/api/v1/admin/payments/$INTENT_ID \
  -H "authorization: Bearer $ADMIN_TOKEN" -H 'content-type: application/json' \
  -d '{"action":"SUCCESS"}'

# Saldo merchant bertambah, invoice → PAID.
```

## Test

```bash
go test ./...
```

Test fokus pada business logic service layer (`internal/service`). Repository di-stub via in-memory mocks.

## Catatan

- `JWT_SECRET` wajib di-set (panjang & random). Aplikasi gagal start kalau kosong.
- `ADMIN_EMAIL`/`ADMIN_PASSWORD` opsional — kalau di-set, akan seed user admin (idempotent).
- Background sweeper menjalankan `MarkExpired` setiap `INVOICE_EXPIRY_CHECK_INTERVAL` (default 1 menit) — invoice PENDING dengan `due_date < now()` dipindah ke EXPIRED.
- Refresh token disimpan **hanya hash-nya** (SHA-256) di DB. Plaintext dikembalikan ke client sekali waktu issue. Pencurian DB read-only tidak menghasilkan kredensial yang bisa dipakai. Setiap kali `/auth/refresh` dipanggil, token lama di-revoke dan diganti pasangan baru (rotation).
