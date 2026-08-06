# Payment Sandbox

Implementasi **backend + frontend** untuk *Payment Sandbox App* (spesifikasi: [`agent_documentation/requirement.md`](agent_documentation/requirement.md)).

| Bagian | Stack | Lokasi |
|---|---|---|
| Backend (SRS §1–3, §5) | Go + Gin + PostgreSQL, clean architecture (handler → service → repository) | repo root |
| Frontend (SRS §4) | React 19 + TypeScript + Vite, SPA | [`web/`](web/) — lihat [web/README.md](web/README.md) |

## Menjalankan Keduanya

Ada dua cara menjalankan backend. Pilih salah satu — keduanya memakai Postgres yang sama.

### Opsi A — semuanya di Docker (paling sedikit langkah)

```bash
cp .env.example .env
sed -i '' "s|^JWT_SECRET=.*|JWT_SECRET=$(openssl rand -hex 32)|" .env   # macOS

docker compose up -d --build         # postgres → migrate → api → web
```

Buka **http://localhost:3000**. Selesai — tidak perlu Go, tidak perlu Node, tidak perlu
`migrate` manual. Compose menunggu tiap dependensi siap sebelum lanjut.

### Opsi B — jalan di host (enak buat iterasi cepat)

```bash
# Terminal 1 — backend
docker compose up -d postgres        # cuma database-nya
cp .env.example .env && set -a; source .env; set +a
go run ./cmd/api migrate
go run ./cmd/api                     # :8080

# Terminal 2 — frontend
cd web && npm install
npm run dev                          # :5173, mem-proxy /api ke :8080
```

Buka http://localhost:5173. Opsi ini yang dipakai saat menulis kode: `go run` restart
dalam ~2 detik dan Vite punya hot reload, sedangkan rebuild image butuh belasan detik.
Keduanya bisa dicampur — misalnya `docker compose up -d postgres api` lalu frontend-nya
`npm run dev`.

Buka http://localhost:5173. Login sebagai admin memakai `ADMIN_EMAIL`/`ADMIN_PASSWORD`
dari `.env`, atau daftar akun merchant baru.

Vite mem-proxy `/api` ke backend sehingga browser tetap satu origin — **tidak ada CORS
yang perlu dikonfigurasi di backend**.

## Stack

- Go 1.22+
- Gin (HTTP)
- `database/sql` + `jackc/pgx/v5` driver
- PostgreSQL
- JWT (HS256) + bcrypt
- `log/slog` untuk structured logging
- `go-playground/validator` (terintegrasi Gin binding)

## Design Documentation

Alasan di balik keputusan teknis ada di [`agent_documentation/`](agent_documentation/) —
bukan cara pakai, tapi *kenapa dibuat begini*:

| Dokumen | Isi |
|---|---|
| [Architecture](agent_documentation/01-architecture.md) | Kenapa berlapis, kenapa repository pakai interface, kenapa tanpa ORM |
| [Data Integrity](agent_documentation/02-data-integrity.md) | **Kenapa harus atomic** — ACID, anomali concurrency, CAS, row lock, studi kasus bug over-refund |
| [State Machines](agent_documentation/03-state-machines.md) | Teori FSM dan kenapa guard-nya ada di dua lapis |
| [Security](agent_documentation/04-security.md) | Kenapa bcrypt (bukan SHA), kenapa dua jenis token, serangan `alg=none` |
| [Testing Strategy](agent_documentation/05-testing-strategy.md) | Apa yang diuji di mana, dan **batas** yang tidak bisa dibuktikan tiap lapis |
| [Trade-offs](agent_documentation/06-trade-offs.md) | Batasan yang diketahui dan apa yang berubah di produksi |
| [Frontend](agent_documentation/07-frontend.md) | Kenapa refresh single-flight, kenapa Zustand, trade-off token storage |

## Design Patterns

- **Repository** untuk abstraksi data access
- **Unit of Work** (`internal/transaction`) untuk operasi atomic (Payment SUCCESS, Refund SUCCESS, Topup SUCCESS)
- **State Machine** — graf transisi terpusat di `internal/constant/statemachine.go` (`InvoiceFSM`, `PaymentFSM`, `RefundFSM`, `TopupFSM`), dieksekusi via CAS-style `UPDATE ... WHERE status = old_status` (transisi invalid → `INVALID_STATE`)
- **Strategy + Factory** untuk metode pembayaran (WALLET / VA_DUMMY / EWALLET_DUMMY)
- **Atomic in-place balance update** pada wallet (`balance = balance + delta`, kolom `version` di-increment sebagai audit counter) + guard `balance + delta >= 0` untuk debit
- **Row-level lock** (`SELECT ... FOR UPDATE`) pada invoice saat request refund, supaya request paralel tidak bisa melebihi sisa refundable
- **Middleware Chain**: request-id → recovery → logger → metrics → error mapper → auth → role
- **Token Bucket Rate Limiter** (`internal/pkg/ratelimit`) — refill kontinu, dua dimensi (per-IP + per-akun)
- **Advisory Lock** (`pg_try_advisory_xact_lock`) untuk memilih satu sweeper per tick di deployment multi-replica

## Folder Structure

```
cmd/api/                # entrypoint, DI wiring, graceful shutdown, expiry sweeper
internal/
  config/               # env loader
  constant/             # constant list
  model/                # struct list
  handler/              # gin handlers + router
  service/              # business logic + payment strategies
  repository/           # interfaces + database/sql impl + dashboard aggregator + migrations/
  middleware/           # auth, role, request-id, logger, error mapper, recovery
  transaction/          # UnitOfWork (database/sql tx bound to context)
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

# JWT_SECRET wajib diganti. Placeholder-nya lolos cek panjang (45 karakter) jadi app
# TETAP START — justru itu bahayanya: nilainya ada di repo, siapa pun bisa memalsukan
# token admin. Generate yang asli:
sed -i '' "s|^JWT_SECRET=.*|JWT_SECRET=$(openssl rand -hex 32)|" .env   # macOS
# sed -i "s|^JWT_SECRET=.*|JWT_SECRET=$(openssl rand -hex 32)|" .env    # Linux

# opsional: ADMIN_EMAIL / ADMIN_PASSWORD utk seed admin pertama kali
```

> **Kalau port 5432 sudah dipakai** (proyek lain, atau Postgres yang terinstall di host),
> `docker compose up` gagal dengan `port is already allocated`. Tidak perlu mematikan
> Postgres yang lain — set `POSTGRES_HOST_PORT` di `.env` dan samakan port di
> `DATABASE_URL`:
>
> ```bash
> POSTGRES_HOST_PORT=5433
> DATABASE_URL=postgres://postgres:postgres@localhost:5433/payment_sandbox?sslmode=disable
> ```
>
> Port di dalam container tetap 5432, jadi hanya dua baris itu yang perlu sepakat.

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

## Menjalankan di Docker

[`Dockerfile`](Dockerfile) multi-stage: build dengan `golang:1.25-alpine`, runtime
`alpine:3.21` berisi satu binary statis (~43 MB, jalan sebagai user non-root `app`).
Migrasi SQL ikut ter-*embed* di binary lewat `go:embed`, jadi tidak ada folder
`migrations/` yang perlu disalin ke image.

```bash
docker compose up -d --build      # build + jalankan semuanya
docker compose logs -f api        # lihat log
docker compose down               # matikan (data tetap)
```

Empat service, dan urutannya dijaga Compose:

| Service | Port host | Peran |
|---|---|---|
| `postgres` | 5432 | Database. Yang lain menunggu sampai `healthy`. |
| `migrate` | — | **One-shot** — migrasi + seed admin, lalu exit 0. |
| `api` | 8080 | Start setelah `migrate` sukses (`service_completed_successfully`). |
| `web` | **3000** | nginx menyajikan SPA hasil build + mem-proxy `/api` ke `api`. |

Migrasi sengaja dipisah jadi service sendiri, bukan digabung ke boot-nya `api`. Kalau
setiap replica menjalankan migrasi saat start, N replica akan berebut DDL yang sama, dan
migrasi yang gagal akan terlihat sebagai app yang crashloop — padahal yang gagal itu
deploy-nya. Memisahkannya membuat kegagalan muncul di tempat yang benar.

`migrate` akan selalu tampil `Exited (0)` di `docker compose ps -a`. Itu normal — service
one-shot yang sudah selesai, bukan error.

### Frontend: `web` (nginx)

[`web/Dockerfile`](web/Dockerfile) multi-stage: build dengan `node:22-alpine`
(`tsc -b && vite build`, jadi type error menggagalkan build image, bukan lolos ke
produksi), runtime `nginx:alpine` yang hanya berisi `dist/` — tanpa `node_modules`, tanpa
source, tanpa toolchain.

Tiga hal yang diatur [`web/nginx.conf`](web/nginx.conf), masing-masing menutup masalah nyata:

| Aturan | Kenapa perlu |
|---|---|
| `try_files $uri $uri/ /index.html` | `BrowserRouter` memiliki path seperti `/merchant/invoices/:id` yang tidak ada sebagai file. Tanpa fallback, refresh atau paste deep-link → 404. |
| Proxy `/api/` → `http://api:8080` | Menjaga browser tetap **satu origin**, persis peran Vite proxy saat dev. Ini yang membuat backend bisa tanpa CORS sama sekali ([07-frontend.md §8](agent_documentation/07-frontend.md)). |
| `Cache-Control` beda untuk asset vs `index.html` | Asset ber-hash boleh di-cache setahun; `index.html` **tidak boleh**, karena dia yang menunjuk ke URL asset — kalau basi, browser terkunci di deploy sebelumnya. |

Satu detail yang gampang salah: header diteruskan dengan
`proxy_set_header X-Forwarded-For $remote_addr` — **menimpa**, bukan `$proxy_add_x_forwarded_for`
yang menambahkan. Backend melakukan rate limit per IP lewat `c.ClientIP()`, yang membaca
header ini. Kalau nilainya ditambahkan, pemanggil bisa mengirim `X-Forwarded-For` sendiri
dan mendapat bucket rate-limit baru tiap request. Karena di sini hop proxy-nya tepat satu,
`$remote_addr` adalah satu-satunya kebenaran.

> ⚠️ Ini hanya melindungi lalu lintas **lewat nginx**. Port `api` juga di-publish ke
> `:8080`, dan Gin secara default memercayai semua proxy — request langsung ke `:8080`
> dengan `X-Forwarded-For` palsu tetap diterima. Lihat [Catatan](#catatan).

### `DATABASE_URL` berbeda di dalam dan di luar Docker

Ini bagian yang paling mudah bikin bingung. Compose **menimpa** `DATABASE_URL` dari `.env`
untuk kedua service Go-nya:

| Menjalankan dari | Host | Port |
|---|---|---|
| Host (`go run ./cmd/api`) | `localhost` | `POSTGRES_HOST_PORT` (port yang di-publish) |
| Dalam Docker | `postgres` (nama service) | `5432` (port **container**, selalu) |

Jadi `POSTGRES_HOST_PORT` sama sekali tidak relevan di dalam jaringan Compose — port
publish cuma ada di sisi host. Itu sebabnya `.env` boleh tetap menunjuk `localhost:5433`
tanpa merusak container.

Port host bisa digeser lewat `.env` kalau bentrok — `POSTGRES_HOST_PORT`, `API_HOST_PORT`,
`WEB_HOST_PORT`. Port di dalam container tidak pernah berubah.

### Setelah mengubah kode

```bash
docker compose up -d --build api     # backend
docker compose up -d --build web     # frontend
docker compose up -d --build         # keduanya
```

`--build` itu wajib. Tanpanya Compose memakai image lama dan perubahan **tidak muncul**,
tanpa error apa pun.

Migrasi baru tidak butuh langkah tambahan: `api` dan `migrate` berbagi image yang sama,
jadi image baru memicu keduanya dibuat ulang dan `migrate` jalan lagi sebelum `api` naik.
Perubahan yang hanya menyentuh `.env` tidak perlu `--build` sama sekali.

### Catatan

- [`.dockerignore`](.dockerignore) dan [`web/.dockerignore`](web/.dockerignore) memangkas
  build context; hampir seluruh 119 MB repo adalah `web/node_modules`, yang tidak dipakai
  image backend sama sekali dan di-install ulang oleh `npm ci` di image frontend.
- `.env` **tidak** ikut masuk image (dikecualikan di kedua `.dockerignore`). Untuk backend
  secret disuntik saat runtime lewat `env_file`. Untuk frontend ini lebih penting lagi:
  Vite meng-*inline* `VITE_*` saat build, jadi `.env` yang terbawa akan tercetak permanen
  ke dalam JS yang dikirim ke browser.
- Healthcheck `api` memakai `/readyz` (cek DB), bukan `/healthz` (liveness, sengaja tidak
  menyentuh DB). Alasannya di [Operational Endpoints](#operational-endpoints).
- **Gin memercayai semua proxy secara default**, jadi `X-Forwarded-For` dari pemanggil
  langsung ke `:8080` diterima apa adanya dan rate limit per-IP bisa dilewati dengan
  memutar nilai header tiap request. nginx sudah menutup jalur lewat `:3000`, tapi
  penutupan sebenarnya adalah `r.SetTrustedProxies([...])` di router — belum diterapkan.
- `web` menyajikan hasil build produksi, **bukan** pengganti `npm run dev`. Hot reload
  tetap hanya ada di Vite dev server.

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
| GET | `/api/v1/pay/:token/intents/:id` | public | payer polling status pembayaran |
| GET | `/api/v1/admin/payments` | admin | cari payment intent (filter `invoice_id`, `status`, pagination) |
| GET | `/api/v1/admin/payments/:id` | admin | detail payment intent |
| PATCH | `/api/v1/admin/payments/:id` | admin | `{"action":"SUCCESS\|FAILED"}` |
| GET | `/api/v1/admin/refunds` | admin | list semua refund |
| PATCH | `/api/v1/admin/refunds/:id` | admin | **single endpoint** — `{"action":"APPROVE\|REJECT\|PROCESS\|FAIL"}` |
| GET | `/api/v1/admin/dashboard` | admin | statistik (filter `merchant_id`, `from`, `to`) |
| GET | `/api/v1/admin/topups` | admin | list semua top-up |
| GET | `/healthz` | public | liveness probe — process only, never checks the DB |
| GET | `/readyz` | public | readiness probe — 503 kalau DB tidak terjangkau |
| GET | `/metrics` | public* | Prometheus histogram latency (`METRICS_ENABLED=true`) |
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

## Operational Endpoints

| Endpoint | Fungsi | Catatan |
|---|---|---|
| `GET /healthz` | Liveness | Hanya melaporkan proses hidup. **Tidak** cek database — kalau ikut cek, satu blip DB akan membuat semua replica gagal liveness dan orchestrator merestart seluruh fleet. |
| `GET /readyz` | Readiness | Cek database (timeout 2s). 503 → load balancer berhenti routing ke instance ini. Detail error hanya masuk log, tidak ke response. |
| `GET /metrics` | Latency histogram | Format Prometheus, bucket rapat di sekitar 300 ms (target §5.1) + gauge `http_requests_within_slo_ratio`. Opt-in via `METRICS_ENABLED`. |

> `/metrics` membocorkan inventaris route dan bentuk trafik. Di produksi batasi ke scraper
> (network policy atau port terpisah), jangan diekspos ke internet.

## Rate Limiting

Dua dimensi, karena keduanya menutup serangan yang berbeda:

| Dimensi | Key | Default | Menutup |
|---|---|---|---|
| Per-IP | `c.ClientIP()` | 30/menit, burst 10 | Satu host mencoba banyak akun |
| Per-akun | email (lower-cased) | 5/menit, burst 5 | Botnet menggerus satu akun dari banyak IP |

Berlaku hanya di `/api/v1/auth/*` — endpoint bisnis sudah dijaga token, dan men-throttle
dashboard merchant yang sah adalah outage yang kita buat sendiri. Health probe juga tidak
di-limit (orchestrator mem-poll terus-menerus).

Melebihi budget → `429 RATE_LIMITED` + header `Retry-After`. Set rate ke `0` untuk
mematikan salah satu dimensi.

> Limiter-nya in-memory per proses, jadi dengan N replica limit efektifnya N×rate. Cukup
> untuk memperlambat penyerang, bukan pengganti limiter di edge/gateway dengan state
> bersama. Lihat [trade-offs](agent_documentation/06-trade-offs.md).

## State Machines

| Entity | Allowed Transitions |
|---|---|
| Invoice | PENDING → PAID / EXPIRED |
| PaymentIntent | PENDING → SUCCESS / FAILED |
| Refund | REQUESTED → APPROVED / REJECTED; APPROVED → SUCCESS / FAILED |
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

### Unit test (tanpa dependensi eksternal)

```bash
go test ./...
go test ./... -cover
```

Cakupan per layer:

| Package | Fokus |
|---|---|
| `internal/service` | business logic + state machine + atomicity (repository di-stub in-memory) |
| `internal/handler` | kontrak HTTP end-to-end lewat router asli: routing, RBAC, validasi, bentuk response |
| `internal/middleware` | auth wajib/opsional, role guard, error mapping, request id, structured log |
| `internal/pkg/*` | jwt (expiry, wrong secret, `alg=none`), bcrypt, token entropy, pagination, error→HTTP mapping |
| `internal/constant` | graf state machine diuji per-edge terhadap diagram SRS §3.4 |
| `internal/model` | mapper: memastikan field sensitif tidak ikut ter-serialize |
| `internal/config` | validasi env (JWT_SECRET, durasi) |

### Integration test (butuh Postgres)

Menguji hal yang tidak bisa dibuktikan mock: SQL sebenarnya, CAS update, guard saldo
non-negatif, `SELECT ... FOR UPDATE`, dan rollback transaksi nyata.

```bash
docker compose up -d postgres
export TEST_DATABASE_URL='postgres://postgres:postgres@localhost:5432/payment_sandbox?sslmode=disable'
go test -tags=integration ./internal/repository/ -race
```

Sesuaikan port-nya kalau Anda men-set `POSTGRES_HOST_PORT` (lihat catatan di
[Konfigurasi env](#2-konfigurasi-env)).

### End-to-end test (butuh stack yang berjalan)

Menguji semua lapis **terkomposisi** lewat HTTP: routing, middleware, handler, service,
SQL, Postgres nyata. Ini yang menangkap route salah path, middleware lupa dipasang, service
di-wire ke repository yang salah, dan efek samping yang tidak ter-rollback — kelas bug yang
lolos dari setiap lapis di bawahnya.

```bash
docker compose -f docker-compose.yml -f docker-compose.e2e.yml up -d --build
export E2E_BASE_URL=http://localhost:8080
go test -tags=e2e ./test/e2e/ -count=1 -race
```

104 skenario lulus. **1 gagal, dan kegagalannya nyata** — lihat
[test/e2e/README.md](test/e2e/README.md) untuk cakupan lengkap, kenapa perlu overlay
compose, dan detail bug yang ditemukannya.

Tanpa `TEST_DATABASE_URL` test ini di-skip, jadi `go test ./...` tetap hijau di mesin
tanpa database.

## Catatan

- `JWT_SECRET` wajib di-set, minimal 32 karakter (sepanjang output HS256). Aplikasi gagal start kalau kosong atau terlalu pendek.
- `ADMIN_EMAIL`/`ADMIN_PASSWORD` opsional — kalau di-set, akan seed user admin (idempotent).
- Background sweeper berjalan setiap `INVOICE_EXPIRY_CHECK_INTERVAL` (default 1 menit): invoice PENDING dengan `due_date < now()` → EXPIRED, lalu payment intent PENDING milik invoice EXPIRED → FAILED (intent itu tidak akan pernah bisa SUCCESS, dan kalau dibiarkan PENDING akan terus mengecilkan `total_failed` di dashboard §2.6).
- Sweeper dilindungi advisory lock transaction-scoped, jadi di deployment multi-replica hanya satu instance yang menyapu per tick. Yang kalah race melewatkan tick-nya (bukan error).
- Refund dibatasi akumulatif: total refund `REQUESTED` + `APPROVED` + `SUCCESS` per invoice tidak boleh melebihi nominal invoice. Refund `REJECTED`/`FAILED` melepas klaimnya sehingga nominalnya bisa dipakai lagi.
- `invoice_number` dan `payment_token` di-generate acak terhadap constraint `UNIQUE`; tabrakan (sangat tidak mungkin) di-retry maksimal 3× alih-alih jadi HTTP 500.
- Refresh token disimpan **hanya hash-nya** (SHA-256) di DB. Plaintext dikembalikan ke client sekali waktu issue. Pencurian DB read-only tidak menghasilkan kredensial yang bisa dipakai. Setiap kali `/auth/refresh` dipanggil, token lama di-revoke dan diganti pasangan baru (rotation).
- **Reuse detection**: token yang sudah di-revoke lalu dipakai lagi = token dipakai dua kali. Itu hanya terjadi kalau token dicuri (atau client mengirim ulang), dan keduanya tidak bisa dibedakan — jadi seluruh rantai token user tersebut di-revoke (`RevokeAllForUser`) dan peristiwanya di-log sebagai `refresh_token_reuse_detected`. Token yang cuma **expired** bukan reuse dan tidak menjatuhkan sesi lain.
- Login menjalankan bcrypt walau email tidak ditemukan (`hash.CompareDecoy`), supaya waktu respons tidak membocorkan keberadaan akun.
