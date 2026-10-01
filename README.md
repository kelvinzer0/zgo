# ZGo v2 — Remote `go install`, Cache-First

[![CI](https://github.com/zgo-cli/zgo/actions/workflows/ci.yml/badge.svg)](https://github.com/zgo-cli/zgo/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

`zgo install <pkg>@<ver>` memasang binary Go tanpa compiler lokal. Build dilakukan di GitHub Actions **sekali saja**, hasilnya di-cache global dan dipakai semua user. Install pertama ±40–90 dtk, install berikutnya (siapa pun) **< 3 dtk**.

**Prinsip:**
1. **Cache-first**: Content-addressed global cache berbasis GitHub Releases (tanpa auth, super cepat via CDN).
2. **Satu perintah tanpa setup**: Single static binary, zero-config, tidak butuh Go lokal.
3. **Hasil terverifikasi**: Verifikasi integritas SHA-256 dan provenance attestation (Sigstore / GitHub Attestations).
4. **Semantics `go install` tetap terjaga**: Resolusi GOBIN dan flag identik dengan Go resmi.

---

## 1. Arsitektur

```
CLI ──resolve──► proxy.golang.org (concrete version + commit)
 │
 ├─ key = sha256(canonical request)
 ├─ cek cache lokal ─────────────────────────► HIT → install
 ├─ cek Release `b-<key>` (HTTP HEAD) ───────► HIT → download → verify → install
 └─ MISS → Broker /build (single-flight)
              └► workflow_dispatch (key sebagai input)
                    └► Build job (tanpa secret) → Publish job → Release `b-<key>`
        CLI ◄── status stream (SSE) ◄── Broker ◄── webhook workflow_run
```

### Dua Mode Operasi
- **Public (default):** Lewat broker tipis (Cloudflare Worker) + builder repo publik milik ZGo. Zero config.
- **Self-hosted:** User memakai fork builder + token GitHub sendiri; mendukung private module, custom `GOPROXY`, dan CGO.

---

## 2. Instalasi ZGo

### Menggunakan Shell Installer (Linux / macOS)
```bash
curl -fsSL https://raw.githubusercontent.com/zgo-cli/zgo/main/install.sh | sh
```

### Manual Binary Download
Unduh binary statis tunggal untuk sistem Anda dari [GitHub Releases](https://github.com/zgo-cli/zgo/releases):
- `linux/amd64`, `linux/arm64`
- `darwin/amd64`, `darwin/arm64` (Apple Silicon & Intel)
- `windows/amd64`, `windows/arm64`

ZGo adalah binary mandiri (**ZGo tidak membutuhkan Go lokal**).

---

## 3. Penggunaan CLI

### `zgo install`
Memasang binary dari module/package Go:
```bash
# Pasang versi terbaru
zgo install github.com/charmbracelet/glow@latest

# Pasang versi spesifik
zgo install github.com/junegunn/fzf@v0.50.0

# Cross-compile target OS dan arsitektur lain
zgo install github.com/jesseduffield/lazygit@latest --os darwin --arch arm64

# Gunakan build tags
zgo install github.com/example/tool@latest --tags integration,netgo

# Bypass local cache
zgo install github.com/example/tool@latest --no-cache

# Mode self-hosted
zgo install gitlab.internal/corp/private-tool@v1.0.0 --self-hosted
```

### `zgo list`
Menampilkan daftar binary yang terpasang melalui ZGo:
```bash
zgo list
# atau format JSON:
zgo list --json
```

### `zgo upgrade`
Memperbarui package yang telah terpasang ke versi terbaru:
```bash
# Upgrade satu package
zgo upgrade github.com/charmbracelet/glow

# Upgrade seluruh package terpasang
zgo upgrade
```

### `zgo uninstall`
Menghapus package dan seluruh file binary-nya dari `$GOBIN`:
```bash
zgo uninstall github.com/charmbracelet/glow
```

### `zgo verify`
Memvalidasi integritas binary di disk terhadap manifest dan attestation rilis remote:
```bash
zgo verify github.com/charmbracelet/glow
```

### `zgo doctor`
Memeriksa status lingkungan, path GOBIN, konektivitas Go Proxy, GitHub CDN, dan Broker:
```bash
zgo doctor
```

### `zgo cache`
Melihat ukuran atau membersihkan cache lokal ZGo:
```bash
zgo cache size
zgo cache path
zgo cache clean
```

---

## 4. Flow `zgo install`

1. **Parse**: `pkg[@ver]` (default `@latest`).
2. **Resolve**: Menemukan module path dan resolver versi konkret + commit via `GET $GOPROXY/<mod>/@v/<ver>.info`. TTL cache `@latest` adalah 5 menit.
3. **Snapshot Environment**:
   - Build-affecting: `GOOS`, `GOARCH`, `GOARM`, `GOAMD64`, `GO386`, `GOMIPS*`, `GOPPC64`, `CGO_ENABLED`, `GOFLAGS` (allowlist), `GOTOOLCHAIN`.
   - Local-only: `GOBIN`, `GOPATH`, `GOMODCACHE`, `GOCACHE`, `GOENV`.
   - Urutan prioritas: Flag CLI → Env Var OS → File `go env -w` (`os.UserConfigDir()/go/env`) → Default Runtime.
4. **Cache Key**: SHA-256 dari representasi kanonikal request (termasuk flags yang ternormalisasi & terurut, schema v2).
5. **Lookup**:
   - Cek cache lokal (`os.UserCacheDir()/zgo`).
   - Cek CDN rilis publik GitHub (`https://github.com/<builder>/releases/download/b-<key>/metadata.json`).
6. **Hit**: Langsung unduh binary mentah, metadata, SHA256SUMS, dan Sigstore bundle (< 3 detik).
7. **Miss**: `POST broker/build {request}`. Broker melakukan single-flight deduplikasi dan mentrigger GitHub Actions `workflow_dispatch`.
8. **Wait (SSE)**: CLI mendengarkan Server-Sent Events dari broker (`queued` → `building` → `publishing` → `completed`), dengan fallback polling setiap 2 detik.
9. **Verify**:
   - SHA-256 binary diverifikasi terhadap `metadata.json` dan `SHA256SUMS`.
   - Commit hash dan versi diverifikasi terhadap hasil resolusi proxy.
   - Build provenance attestation (Sigstore / in-toto) diverifikasi berasal dari workflow resmi `builder.yml`.
   - **Jika verifikasi gagal, binary tidak dipasang.**
10. **Install Atomik**: Ditulis ke file temporary di `GOBIN`, disetel `chmod 0755`, lalu di-rename ke nama binary target. Manifest lokal di `installed.json` diperbarui secara atomik. Peringatan diberikan jika `GOBIN` belum terdaftar di `$PATH`.

---

## 5. Keamanan & Allowlist GOFLAGS

Untuk mencegah eksploitasi Remote Code Execution (RCE) di builder CI, seluruh nilai `GOFLAGS` divalidasi dengan allowlist ketat:

| Status | Flag |
| --- | --- |
| **Diizinkan** | `-tags`, `-trimpath`, `-buildvcs`, `-ldflags` (hanya `-s -w -X`), `-gcflags` (terbatas) |
| **Ditolak (Error)** | `-toolexec`, `-exec`, `-modfile`, `-overlay`, `-mod=vendor`, `-pkgdir`, `-extldflags`, path filesystem lokal |

Semua flag yang tidak ada dalam allowlist langsung ditolak sebelum build dipicu (`ERROR: unsupported GOFLAGS`).

---

## 6. Struktur Repository

```
zgo/
├── cmd/
│   └── zgo/                  # Entry point CLI
├── internal/
│   ├── attestation/          # Verifikasi Sigstore & in-toto attestation
│   ├── cache/                # Cache manager lokal (UserCacheDir/zgo)
│   ├── canonical/            # Canonical request & komputasi key sha256
│   ├── client/               # Release CDN client, SSE stream, broker client
│   ├── commands/             # Handler perintah: install, list, upgrade, dll.
│   ├── config/               # Manajemen konfigurasi CLI
│   ├── env/                  # Resolusi environment & GOBIN
│   ├── installer/            # Instalasi atomik ke GOBIN & update manifest
│   ├── manifest/             # Local installation store (UserStateDir/zgo/installed.json)
│   └── resolve/              # Proxy module resolver & TTL caching
├── broker/                   # Cloudflare Worker Edge Broker
│   ├── src/                  # Single-flight deduplication, SSE, & Webhook
│   ├── wrangler.jsonc        # Worker config
│   └── package.json
├── .github/
│   └── workflows/
│       ├── builder.yml       # Builder CI (CGO=0, Sigstore provenance attestation)
│       ├── ci.yml            # CI test multi-OS
│       └── release.yml       # Release ZGo binary
├── install.sh                # Universal POSIX installer
└── Makefile                  # Build & test automation
```

---

## 7. Pengembangan & Kontribusi

Jalankan test lokal:
```bash
make test
```

Build binary:
```bash
make build
```

Jalankan diagnostic:
```bash
make doctor
```

---

## 8. Lisensi

MIT License © 2026 ZGo Contributors.
