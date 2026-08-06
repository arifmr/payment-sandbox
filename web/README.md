# Payment Sandbox — Frontend

Implementasi SRS §4 (Frontend). React + TypeScript + Vite, SPA client-side.

Alasan di balik keputusan teknisnya ada di
[`../agent_documentation/07-frontend.md`](../agent_documentation/07-frontend.md).

## Stack

| Bagian | Pilihan | Alasan singkat |
|---|---|---|
| Build | Vite 7 | Dev server cepat + proxy bawaan (tidak perlu CORS di backend) |
| UI | React 19 + TypeScript strict | `noUncheckedIndexedAccess` & `verbatimModuleSyntax` aktif |
| Routing | react-router-dom 7 | Routing berbasis komponen, nested route untuk layout |
| State | Zustand 5 | Hanya sesi yang benar-benar global; Redux berlebihan untuk satu objek |
| Styling | CSS + custom properties | Tanpa framework — token di satu file, light/dark jadi swap token |
| Test | Vitest + React Testing Library | Query berbasis peran/label, bukan class internal |

## Menjalankan

```bash
npm install
cp .env.example .env      # opsional; default sudah benar untuk dev
npm run dev               # http://localhost:5173
```

Backend harus jalan di `http://localhost:8080` (lihat [README utama](../README.md)).
Vite mem-proxy `/api` ke sana, jadi browser tetap satu origin — **tidak ada CORS yang
perlu dikonfigurasi di backend**.

Arahkan ke backend lain:

```bash
VITE_API_TARGET=http://localhost:9090 npm run dev
```

Kalau frontend disajikan dari origin berbeda (bukan lewat proxy), set `VITE_API_BASE_URL`
dan aktifkan CORS di backend.

## Menjalankan hasil build di Docker

```bash
docker compose up -d --build web     # dari root repo
```

Buka http://localhost:3000. [`Dockerfile`](Dockerfile) build dengan `node:22-alpine` lalu
menyajikan `dist/` lewat `nginx:alpine`; [`nginx.conf`](nginx.conf) menangani SPA fallback
dan mem-proxy `/api` ke container backend — peran yang sama seperti Vite proxy di dev,
sehingga browser tetap satu origin dan backend tetap tanpa CORS.

Ini untuk **memverifikasi hasil build**, bukan pengganti `npm run dev`: tidak ada hot
reload, dan tiap perubahan butuh `--build` ulang. Selama menulis kode, tetap pakai dev
server. Detail lengkap ada di [README utama](../README.md#menjalankan-di-docker).

## Skrip

```bash
npm run dev            # dev server
npm run build          # tsc -b && vite build  -> dist/
npm run preview        # serve hasil build
npm run typecheck      # tsc tanpa emit
npm test               # vitest run
npm run test:watch     # vitest watch
npm run test:coverage  # vitest + laporan coverage
```

## Struktur

```
src/
  api/         client HTTP (single-flight refresh) + endpoint bertipe + tipe wire
  store/       auth.ts — sesi (Zustand, persisted)
  hooks/       useAsync / useAction / usePaginatedList / useCopy
  lib/         format.ts (uang, tanggal, label) + validation.ts (SRS §4.5)
  components/
    ui/        Button, Field, Card, Badge, Table, Pagination, StateView, CopyField, …
    layout/    AppShell, PageHeader, RequireAuth
  pages/
    LoginPage, RegisterPage, NotFoundPage
    merchant/  Dashboard, InvoiceList, InvoiceCreate, InvoiceDetail, Wallet, Refunds
    pay/       PaymentPage — halaman publik untuk pembayar
    admin/     Dashboard, Payments, Refunds, Topups
  styles/      tokens.css (design token) + global.css (reset & utilitas)
  test/        setup, helper render, polyfill localStorage
```

## Peta Requirement → Halaman

| SRS | Halaman / modul |
|---|---|
| §2.1 / §4.1 auth & role | `LoginPage`, `RegisterPage`, `store/auth.ts`, `RequireAuth` |
| §4.2 Dashboard invoice | `merchant/MerchantDashboardPage`, `merchant/InvoiceListPage` |
| §4.2 Create invoice form | `merchant/InvoiceCreatePage` |
| §4.2 Invoice detail | `merchant/InvoiceDetailPage` |
| §4.2 Payment link preview & copy | `InvoiceDetailPage` + `components/ui/CopyField` |
| §4.2 Wallet balance & top-up | `merchant/WalletPage` |
| §4.2 Refund request UI | `InvoiceDetailPage` (form) + `merchant/RefundsPage` (riwayat) |
| §4.3 Halaman pembayaran end user | `pay/PaymentPage` |
| §4.4 Payment simulation panel | `admin/AdminPaymentsPage` |
| §4.4 Refund management | `admin/AdminRefundsPage` |
| §4.4 Dashboard statistik | `admin/AdminDashboardPage` |
| §4.1 loading / error / empty | `components/ui/StateView` (`AsyncBoundary`) |
| §4.5 Form validation | `lib/validation.ts` |
| §5.3 Reusable components | `components/ui/*` |

## Rute

| Path | Akses | Isi |
|---|---|---|
| `/login`, `/register` | publik | Masuk & daftar merchant |
| `/pay/:token` | **publik** | Halaman pembayaran — pembayar tidak punya akun |
| `/merchant`, `/merchant/*` | MERCHANT | Dashboard, invoice, wallet, refund |
| `/admin`, `/admin/*` | ADMIN | Dashboard, simulasi pembayaran, refund, top-up |

Guard di `RequireAuth` adalah **navigasi, bukan keamanan** — setiap endpoint tetap
dijaga JWT + `RequireRole` di backend.

## Dua hal yang perlu diketahui

**1. Refresh token itu single-flight.** Backend menganggap refresh token yang dipakai dua
kali sebagai bukti pencurian dan mencabut **seluruh** sesi user. Kalau beberapa request
kedaluwarsa bersamaan dan masing-masing memanggil `/auth/refresh`, yang kedua memicu
pertahanan itu. Karena itu `ApiClient` membagi satu promise refresh yang sedang berjalan.
Gejala kalau ini salah: "user ter-logout acak saat trafik ramai", dan sangat sulit dilacak.

**2. Token disimpan di localStorage.** Artinya XSS di origin ini bisa membacanya.
Alternatifnya (cookie httpOnly) tidak bisa dilakukan dari sisi client — backend harus
men-set-nya plus proteksi CSRF. Trade-off ini dipilih sadar dan dicatat, bukan
disembunyikan. Mitigasi yang sudah ada: access token 15 menit, refresh token sekali pakai
dengan reuse detection.

## Test

```bash
npm test
npm run test:coverage
```

Yang diuji dan **kenapa** ada di
[`../agent_documentation/07-frontend.md`](../agent_documentation/07-frontend.md).
Ringkasnya: `api/`, `lib/`, `store/`, dan alur requirement (login, buat invoice, top-up,
halaman pembayaran, panel admin) tertutup baik; halaman yang murni presentasional belum.

## Catatan dependensi

`react-router-dom` 7.18.1 masih terkena satu advisory: **RSC Mode CSRF Bypass**
(`>=7.12.0 <8.3.0`). Fix-nya di 8.3.0 yang belum dirilis.

Versi ini tetap dipakai karena **justru yang paling sedikit terdampak**: menurunkan ke
7.11.0 memang keluar dari rentang advisory itu, tapi masuk ke belasan advisory lain —
termasuk open redirect di `<Link>`/`useNavigate` dan DoS route matching, yang keduanya
berlaku untuk SPA biasa. Advisory yang tersisa hanya menyentuh RSC mode (React Server
Components), dan aplikasi ini SPA murni dengan `BrowserRouter`: tidak ada server runtime,
tidak ada server action. Perlu ditinjau ulang ketika 8.3.0 keluar.
