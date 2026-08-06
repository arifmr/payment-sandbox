# End-to-End Test Suite

Menguji aplikasi yang **sedang berjalan** lewat HTTP — router, middleware, handler,
service, SQL, dan Postgres nyata, semuanya sekaligus.

Filosofinya sama seperti [05-testing-strategy.md](../../agent_documentation/05-testing-strategy.md):
*test lulus = hal yang diuji test itu benar*, sisanya belum diketahui. Jadi bagian
terpenting dokumen ini adalah tabel di bawah.

## Posisinya di antara lapis test lain

| Lapis | Membuktikan | **Tidak bisa** membuktikan |
|---|---|---|
| `internal/service` (unit) | Aturan bisnis, guard FSM | SQL-nya benar. Lock bekerja. **Rollback benar-benar mengurungkan tulisan.** |
| `internal/handler` (unit) | Routing, RBAC, bentuk JSON | Business logic-nya benar (di-stub) |
| `internal/repository` (integration) | SQL nyata, CAS, row lock, rollback | Aturan bisnis di atasnya |
| **`test/e2e` (di sini)** | Semuanya **terkomposisi**, seperti yang dilihat client | Bahwa suatu fungsi benar *secara terpisah* |

Yang hanya bisa ditangkap di sini:

- Route terdaftar di path yang salah, atau tidak terdaftar sama sekali
- Middleware lupa dipasang di satu grup
- Service di-wire ke repository yang salah di `main.go`
- Tag JSON berubah sehingga kontrak client rusak
- Efek samping yang **tidak** ter-rollback padahal request-nya gagal

Yang **tidak** bisa: diagnosis. Kegagalan di sini berarti "sistemnya salah", bukan "fungsi
X salah". Untuk itu lapis yang lebih sempit tetap dibutuhkan — suite ini melengkapi,
bukan menggantikan.

## Cara menjalankan

Suite di-skip total tanpa `E2E_BASE_URL`, sama seperti integration test di-skip tanpa
`TEST_DATABASE_URL`. Jadi `go test ./...` tetap hijau di mesin yang tidak menjalankan apa pun.

```bash
# 1. Nyalakan stack dengan overlay e2e
docker compose -f docker-compose.yml -f docker-compose.e2e.yml up -d --build

# 2. Jalankan
export E2E_BASE_URL=http://localhost:8080
go test -tags=e2e ./test/e2e/ -count=1

# verbose + race detector (ada beberapa test konkurensi)
go test -tags=e2e ./test/e2e/ -count=1 -v -race
```

### Kenapa butuh overlay `docker-compose.e2e.yml`

Endpoint `/auth/*` di-rate-limit (30/menit per IP). Setiap skenario mendaftarkan
merchant-nya **sendiri** supaya terisolasi — dan itu memakan 2 panggilan auth. Beberapa
puluh skenario menghabiskan budget itu hanya untuk setup, sehingga suite-nya lebih banyak
menunggu limiter daripada menguji apa pun.

[Overlay-nya](../../docker-compose.e2e.yml) melonggarkan limit itu, memperpendek interval
sweeper, dan menurunkan `BCRYPT_COST` (cost 12 itu benar untuk produksi dan sengaja lambat;
di sini kelambatannya mendominasi runtime tanpa membuktikan apa pun yang belum dibuktikan
`pkg/hash`).

Limit-nya dilonggarkan di overlay, **bukan** di `.env`, supaya default yang di-ship tetap
jujur: stack normal tetap menegakkannya, hanya run yang eksplisit opt-in yang dapat yang longgar.

Klien test tetap punya backoff 429 sebagai jaring aman, jadi suite ini **tetap benar**
tanpa overlay — hanya jauh lebih lambat.

### Menjalankan skenario rate limit

Skenario itu butuh limit asli, jadi tidak bisa jalan bersamaan dengan yang di atas:

```bash
docker compose up -d --build          # tanpa overlay
export E2E_BASE_URL=http://localhost:8080
export E2E_STRICT_RATELIMIT=1
go test -tags=e2e ./test/e2e/ -count=1 -run RateLimit
```

Tanpa `E2E_STRICT_RATELIMIT=1` skenario itu di-skip dengan alasan yang tercetak, bukan
diam-diam lulus.

### Variabel lingkungan

| Variabel | Default | Fungsi |
|---|---|---|
| `E2E_BASE_URL` | — | **Wajib.** Tanpa ini seluruh suite di-skip. |
| `E2E_ADMIN_EMAIL` | `admin@example.com` | Admin hasil seed `go run ./cmd/api migrate` |
| `E2E_ADMIN_PASSWORD` | `admin12345` | idem |
| `E2E_STRICT_RATELIMIT` | tidak diset | `1` = target memakai limit asli; buka skenario rate limit |

## Cakupan

| File | Skenario |
|---|---|
| [`payment_test.go`](payment_test.go) | Happy path, settle ganda (CAS), settle **konkuren**, intent FAILED tidak menutup invoice, WALLET debit dua arah, overdraft ditolak + rollback, invoice lewat jatuh tempo, scoping token |
| [`refund_test.go`](refund_test.go) | Siklus dua tahap, **cap kumulatif** (bug over-refund), klaim PENDING di-reserve, REJECTED/FAILED melepas klaim, request **konkuren** (row lock), approval wajib, invoice belum lunas, invoice milik orang lain |
| [`auth_test.go`](auth_test.go) | Rotasi token, **reuse detection**, token asing tidak mencabut apa pun, logout, anti-enumerasi akun, email duplikat, validasi input, hash tidak pernah keluar |
| [`security_test.go`](security_test.go) | Matriks RBAC dua arah, 403 tanpa efek samping, token rusak/palsu, **`alg=none`**, mass assignment, kebocoran field di halaman publik, envelope error |
| [`wallet_test.go`](wallet_test.go) | Top-up kredit hanya saat approve, FAILED tidak kredit, approve **konkuren**, nominal harus positif, scoping |
| [`invoice_test.go`](invoice_test.go) | Keunikan `invoice_number`/`payment_token`, entropi token, validasi, list + filter + pagination, `page_size` dibatasi, pagination rusak |
| [`dashboard_test.go`](dashboard_test.go) | Agregat per-merchant, refund PENDING bukan uang keluar, filter tanggal, admin-only |
| [`ratelimit_test.go`](ratelimit_test.go) | Burst → 429 + `Retry-After`, health probe & endpoint bisnis tidak di-limit |

### Prinsip yang dipakai

**Assertion tidak berhenti di status code.** Setiap aksi yang memindahkan uang juga
memeriksa **saldo**. 403 atau 422 yang dikembalikan *setelah* uangnya pindah tetap
kegagalan finansial — dan itu justru kelas bug yang paling mahal.

**Saldo dibandingkan absolut, bukan relatif.** `balance == 50000`, bukan "naik 50000".
Assertion relatif meloloskan double-credit; absolut tidak. Ini juga alasan setiap skenario
membuat merchant sendiri, bukan berbagi satu.

**Tipe wire ditulis ulang, tidak diimpor dari `internal/model`.** Ini kontrak sebagaimana
dilihat *client*, jadi tag JSON yang berubah harus **memecahkan** suite ini, bukan diikuti
diam-diam. Alasannya sama seperti tabel FSM ditranskrip dari SRS alih-alih diimpor dari kode
produksi.

**Konkurensi di-assert pada hasil, bukan urutan.** Test race melepas N goroutine sekaligus
lalu memeriksa jumlah yang sukses dan saldo akhirnya — bukan siapa yang menang.

**Serangan diuji secara aktif.** `alg=none`, token palsu, mass assignment, akses lintas
merchant. Kerentanan seperti ini tidak muncul di jalur normal sama sekali: sistem yang
`alg=none`-nya terbuka lebar berperilaku sempurna untuk trafik yang sah.

## Status: 1 test gagal, dan kegagalannya nyata

`TestE2E_Auth_ReuseOfARevokedTokenKillsEverySession` **GAGAL**. Itu bukan test yang flaky
atau salah tulis — itu bug produksi yang ditemukan suite ini.

`authService.Refresh` memanggil `RevokeAllForUser` **di dalam** `uow.Do`, lalu
mengembalikan `apperror.ErrUnauthorized` untuk menolak pemanggil. `sqlUoW.Do` melakukan
`Rollback()` pada error apa pun — jadi penolakannya mengurungkan pencabutan yang baru saja
dilakukan. Baris `slog.Warn` tetap jalan, sehingga log melaporkan
`action=revoked_all_sessions_for_user` padahal semua sesi masih hidup.

Terverifikasi langsung di database: setelah reuse dipicu, token lain milik user itu masih
`revoked_at IS NULL`.

Unit test-nya lulus karena memakai `noopUoW`, yang hanya menjalankan callback dan
meneruskan error-nya tanpa transaksi — jadi ia bisa membuktikan `RevokeAllForUser`
**dipanggil**, tidak pernah bahwa tulisannya **selamat**. Batasan itu memang sudah
dinyatakan di [05-testing-strategy.md §3](../../agent_documentation/05-testing-strategy.md);
ini contohnya terjadi sungguhan.

Assertion-nya **sengaja tidak dilemahkan** agar suite hijau. Melemahkannya berarti
menjadikan bug ini sebagai spesifikasi.

## Catatan

- Suite ini **menulis** ke database yang ditunjuknya. Jangan arahkan ke data yang Anda
  sayangi. Datanya tidak dibersihkan: record yang ditinggalkan berguna untuk diperiksa
  setelah kegagalan, dan `docker compose down -v` mengembalikan ke nol.
- Build tag `e2e` membuat file-file ini tidak dikompilasi pada `go test ./...` sama sekali —
  terverifikasi: `go test ./...` exit 0 dan tidak menyebut paket ini. Agar tidak diam-diam
  rusak, sertakan `go vet -tags=e2e ./test/e2e/` di alur verifikasi.
- `go test ./test/e2e/` **tanpa** `-tags=e2e` gagal dengan `[setup failed]`. Itu bukan
  kerusakan — semua file dikecualikan build constraint, jadi Go melihat paket tanpa file.
  Selalu sertakan tag-nya.
- `TestE2E_Payment_PastDueInvoiceIsNotPayable` sengaja `sleep` ~4 detik. Jatuh tempo yang
  sudah lewat tidak bisa dibuat lewat API (dan memang seharusnya tidak bisa), jadi
  satu-satunya cara adalah menunggunya lewat.
