# PrivaSync – Technische Spezifikation

Design-Spec: stabile Architektur-Beschreibung. Implementierungs-Stand,
Roadmap und offene Defekte leben im
[GitHub-Tracker](https://github.com/Jeansy91/priva-sync/issues) (Issues +
Milestones). Begründungen für
einzelne Design-Entscheidungen liegen als ADRs unter [`docs/adr/`](docs/adr/).

## 1. Projektziel

PrivaSync ist eine selbst gehostete Dateisynchronisierungslösung für
Privatpersonen und kleine Teams. Dateien werden bidirektional zwischen
mehreren Clients und einem zentralen Server synchronisiert. Der Server
speichert die Dateien als vollständige Spiegelung des Client-Dateisystems
im Klartext, sodass sie direkt über den Dateiexplorer des Hosts einsehbar
sind.

**Designziele:**
- Korrektheit vor Geschwindigkeit — kein Datenverlust, auch bei Konflikten
- Skalierbarkeit auf Millionen von Dateien
- Self-Hosting via Docker Compose
- WebDAV-Kompatibilität für Rclone und andere Clients

## 2. Repository-Struktur

Monorepo mit pnpm-Workspaces für JS/TS, separaten Go- bzw. Cargo-Modulen.

```
priva-sync/
├── apps/
│   ├── server/    # Go – REST API, WebDAV, Sync Engine
│   ├── web/       # React 19 + TS – Web-Frontend
│   └── desktop/   # Tauri – Desktop-Client
├── packages/
│   ├── ui/        # Shared Design-System (shadcn/ui)
│   └── api-client/# Typsichere API-Bindings
├── mobile/        # Flutter (spätere Phase)
├── docs/
│   └── adr/       # Architecture Decision Records
├── docker-compose.yml
└── Makefile
```

Domänenbegriffe und Glossar: siehe [`docs/glossary.md`](docs/glossary.md),
Architekturüberblick in [`docs/architecture.md`](docs/architecture.md).

## 3. Systemüberblick

```
┌─────────────────────────────────────────────────────┐
│                     Server (Go)                     │
│  REST API ─ WebDAV ─ Sync Engine                    │
│              │                                      │
│       ┌──────┴──────┐         ┌──────────────────┐  │
│       │ PostgreSQL  │         │ /data/{user}/... │  │
│       │ (Index/Cache)│        │ (Klartext-FS)    │  │
│       └─────────────┘         └──────────────────┘  │
└─────────────────────────────────────────────────────┘
        ▲               ▲               ▲
  ┌─────┴────┐    ┌─────┴────┐    ┌────┴───────┐
  │ Desktop  │    │ Browser  │    │ Rclone /   │
  │ (Tauri)  │    │ (React)  │    │ WebDAV     │
  └──────────┘    └──────────┘    └────────────┘
```

## 4. Technologie-Stack

| Komponente       | Technologie                          |
| ---------------- | ------------------------------------ |
| Server           | Go                                   |
| Datenbank        | PostgreSQL                           |
| Web-Frontend     | React 19 + TypeScript + Vite         |
| UI-System        | shadcn/ui + Tailwind (`@privasync/ui`) |
| Dateilisting     | TanStack Table + TanStack Virtual    |
| Server-Push      | SSE                                  |
| Desktop-Client   | Tauri (Rust + WebView)               |
| Mobile (Phase 2) | Flutter                              |
| Deployment       | Docker Compose                       |

## 5. Server

### 5.1 REST-API (Port 8080)

Endpunkt-Übersicht (vollständige OpenAPI-Spec generiert
`packages/api-client/`):

**Auth** — `POST /auth/login`, `POST /auth/logout`, `POST /auth/2fa`

**Dateien** — `GET /files/{root_id}/{path}`, `PATCH /files/{root_id}` (move),
`PATCH /files/{root_id}/{path}/permissions`

**Sync** — `GET /sync/changes?since=`, `PUT /sync/upload/{root_id}/{path}`
(mit `If-Match`), `GET /sync/download/...`, Chunked-Upload via
`POST /sync/upload/init` + `PUT .../chunk/{i}` + `POST .../complete` +
`GET .../status`, Delta via `GET /sync/delta-request`. Das Complete **nimmt
nur an** (`202`); zusammengesetzt wird in einem eigenen Worker, das Ergebnis
holt der Client über `GET .../status?wait=` ab (ADR-0043).

**Konflikte** — `GET /sync/conflicts`, `POST /sync/conflicts/{id}/resolve`

**Papierkorb** — `GET /trash`, `POST /trash/{id}/restore`, `DELETE /trash/{id}`

**Freigaben** — `POST /shares`, `GET /shares/{token}`

**Admin** — `GET /admin/users`

### 5.2 WebDAV (Port 8081)

RFC-4918, Basic Auth oder Bearer. Direkter Zugriff auf den User-Dateibaum,
kompatibel mit Rclone, Windows-Netzlaufwerk, macOS Finder.

### 5.3 Sync Engine

Mutationen laufen über den **Sync Coordinator** (siehe
[`docs/glossary.md`](docs/glossary.md)). Side-Effects landen
transaktional in der **Outbox** und werden von einem In-Process-Worker
ausgeführt (Rename → SSE → Mail, geordnet pro `group_id`).

Delta-Sync-Protokoll: siehe [ADR-0002](docs/adr/0002-delta-sync-blake3.md).
Konflikt-Modell: siehe [ADR-0003](docs/adr/0003-conflict-loser-copy.md).

### 5.4 Datei-Layout

```
/data/
  {user_id}/{sync_root_path}/...     ← Spiegelung des Client-FS
                              .trash/{uuid}/...
  users/{user_id}.json               ← Sidecar
  sync_roots/{root_id}.json          ← Sidecar
  shares/{share_id}.json             ← Sidecar
  .chunks/{upload_session_id}/       ← temporäre Chunks
```

Dateien werden **immer vollständig im Klartext** gespeichert (kein
Block-Store). JSON-Sidecars halten kritische Entitäts-Metadaten redundant
am FS, sodass `cmd/recover` die DB nach Totalverlust aus `/data`
rekonstruieren kann: siehe
[ADR-0001](docs/adr/0001-fs-resilience-sidecars.md).

### 5.5 Datei-Index (PostgreSQL)

PostgreSQL ist ein **rekonstruierbarer Cache**. Zentrale Tabelle `files`:

```sql
CREATE TABLE files (
    id            UUID PRIMARY KEY,
    user_id       UUID NOT NULL REFERENCES users(id),
    sync_root_id  UUID NOT NULL REFERENCES sync_roots(id),
    path          TEXT NOT NULL,
    size          BIGINT NOT NULL,
    mtime         TIMESTAMPTZ NOT NULL,
    content_hash  BYTEA NOT NULL,            -- BLAKE3
    block_hashes  JSONB,                     -- für Delta-Sync
    updated_at    TIMESTAMPTZ NOT NULL,
    UNIQUE(user_id, sync_root_id, path)
);
CREATE INDEX idx_files_path ON files USING hash(path);
CREATE INDEX idx_files_hash ON files(content_hash);
```

Weitere Tabellen: `users`, `sync_roots`, `client_states`, `conflicts`,
`shares`, `share_permissions`, `audit_log`.

## 6. Desktop-Client (Tauri / Rust)

Eigene Sync-Engine in Rust, keine Rclone-Wrappung: siehe
[ADR-0006](docs/adr/0006-own-sync-engine.md).

- **File-Watcher** — `notify`-Crate (inotify / FSEvents /
  `ReadDirectoryChangesW`), 250 ms Debounce, ignoriert `.tmp`/`~`/`.part`
  und `.privasyc/`.
- **Lokaler State** — SQLite `local_files(path, size, mtime, content_hash,
  sync_status, server_hash, updated_at)`; `sync_status ∈ {synced, pending,
  conflict, ignored}`.
- **Upload-Flow** — Hash → Vergleich mit `server_hash` → bei Diff
  Delta-Sync oder Full-Upload.
- **Download-Flow** — SSE-Event → lokalen Hash prüfen → anwenden oder
  Konflikt eskalieren.
- **Cloud Files (Windows)** — WinRT
  `StorageProviderSyncRootManager.Register`, siehe
  [ADR-0005](docs/adr/0005-cloud-files-winrt.md).
- **Tray** — Idle / Syncing / Konflikt / Offline; Rechtsklick: Öffnen,
  Status, Konflikte, Einstellungen, Beenden; Autostart-Toggle.
- **Plattformen** — Windows 10/11, macOS 12+, Linux (X11 + Wayland).

## 7. Web-Frontend

React 19 + TypeScript + Vite, UI über `@privasync/ui` (shadcn/ui +
Tailwind). Konvention für Dropdowns: siehe [`docs/development.md`](docs/development.md).

**Layout** — AppShell (Sidebar + Header + Content), PageHeader,
SectionCard. Dark Mode per `class`-Strategy. Mobile-first Breakpoints
`sm/md/lg/xl`.

**Dateibrowser** — Sidebar-Tree, TanStack-Table mit virtualisiertem
Scrollen, Spalten Icon/Name/Größe/Geändert/Geteilt/Aktionen, Tabelle ↔
Kachelansicht, Drag-&-Drop-Upload, Kontextmenü.

**Seiten** — Login, Dateibrowser, Freigaben, Konflikte, Einstellungen,
Admin.

**Vorschau** — Bilder, Videos (HTML5), PDFs (Browser-PDF), Text/Code mit
Syntax-Highlighting. OnlyOffice für Office-Dokumente: spätere Phase.

**Freigaben** — öffentliche Links (`/share/{uuid}`, optional Passwort/
Ablauf), Benutzer-Shares (read / read+write, erscheinen unter „Geteilt mit
mir").

## 8. Authentifizierung & Sicherheit

- **Login** — `POST /auth/login` → Argon2id-Check → bei 2FA aktiv
  `{status: "2fa_required", session_token}` → `POST /auth/2fa` mit TOTP →
  JWT Access (15 min) + Refresh (30 d, HttpOnly Cookie).
- **2FA** — TOTP (RFC 6238), QR-Setup im Web, 10 Backup-Codes.
- **Transport** — ausschließlich HTTPS; Reverse Proxy (Caddy/Nginx)
  empfohlen.
- **Rate-Limit** — max. 5 Login-Versuche/Min pro IP.

### 8.1 HTTP Security Headers

```
Strict-Transport-Security: max-age=31536000; includeSubDomains
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
Referrer-Policy: strict-origin-when-cross-origin
Content-Security-Policy: default-src 'self'; script-src 'self';
  style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:
Permissions-Policy: camera=(), microphone=(), geolocation=()
```

### 8.2 Audit-Log

```sql
CREATE TABLE audit_log (
    id         UUID PRIMARY KEY,
    user_id    UUID REFERENCES users(id),
    event_type TEXT NOT NULL,
    ip_address INET,
    user_agent TEXT,
    details    JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

Protokolliert: Login (ok/fail), Logout, Passwort/2FA-Änderung, Datei-
Delete, Share-Create/Delete, Admin-Aktionen, Token-Refresh.

### 8.3 Release-Gate

`make security-audit` läuft vor jedem Release (SAST, Container-Scan,
Secret-Scan, DAST, KI-Review). Kritische/hohe Findings blockieren. Siehe
[ADR-0007](docs/adr/0007-security-audit-release-gate.md).

## 9. E-Mail

SMTP-Konfiguration via `config.yaml` oder Env. Trigger:

| Ereignis                | Empfänger                              |
| ----------------------- | -------------------------------------- |
| Registrierung           | neuer User (Willkommen + temp. Passwort) |
| Passwort-Reset          | User (Reset-Link, 1 h)                 |
| Neuer Share empfangen   | Empfänger (registrierter User)         |
| Sync-Konflikt           | betroffener User (opt-in)              |
| 2FA deaktiviert         | betroffener User (Security-Hinweis)    |
| Quota-Threshold gekreuzt | User (80 %, 95 %)                     |

Nur transaktional, kein Marketing. Pro User opt-in/opt-out in den
Einstellungen.

## 10. Nutzerrollen & Quota

| Eigenschaft               | User | Admin |
| ------------------------- | ---- | ----- |
| Eigene Dateien verwalten  | ✓    | ✓     |
| Shares erstellen          | ✓    | ✓     |
| Dateien anderer User sehen | ✗   | ✓     |
| User anlegen/deaktivieren | ✗    | ✓     |
| Quotas setzen             | ✗    | ✓     |
| Audit-Log einsehen        | ✗    | ✓     |

**Quota-Modell** — `UsedBytes + TrashBytes` zählen gegen `QuotaBytes`
(HTTP 507 bei Überlauf). Trash hat keinen eigenen Topf — Begründung in
[ADR-0004](docs/adr/0004-trash-counts-toward-quota.md). Threshold-Mails
bei 80 %/95 % effektiver Auslastung; State-Maschine siehe
[`docs/glossary.md`](docs/glossary.md) (Stichwort **Quota
State**).

## 11. Mobile (Phase 2)

Flutter, iOS + Android. Phase-2-Scope:
- Login mit 2FA
- Dateibrowser (read-only)
- Foto-/Video-Upload vom Gerät (unidirektional, Kamera-Backup)
- Download + lokales Cachen
- Öffentliche Links öffnen

Nicht in Phase 2: bidirektionaler Sync, Konfliktauflösung.

## 12. Deployment

```yaml
services:
  server:
    image: privasync-server
    ports: ["8080:8080", "8081:8081"]
    volumes: ["./data:/data", "./config:/config"]
    environment: ["DATABASE_URL=postgres://..."]
    depends_on: [db]
  db:
    image: postgres:16-alpine
    volumes: ["./pgdata:/var/lib/postgresql/data"]
    environment:
      - POSTGRES_DB=privasync
      - POSTGRES_USER=privasync
      - POSTGRES_PASSWORD=${DB_PASSWORD}
  caddy:  # optional Reverse-Proxy
    image: caddy:2-alpine
    ports: ["80:80", "443:443"]
    volumes: ["./Caddyfile:/etc/caddy/Caddyfile", "caddy_data:/data"]
```

### Konfiguration (`config.yaml`)

```yaml
server:
  host: "0.0.0.0"
  api_port: 8080
  webdav_port: 8081
  base_url: "https://sync.example.com"

storage:
  data_path: "/data"
  max_upload_size_mb: 10240

sync:
  delta_threshold_mb: 1
  block_size_mb: 1

auth:
  jwt_secret: "${JWT_SECRET}"
  access_token_ttl: "15m"
  refresh_token_ttl: "720h"
  refresh_rotation_grace: "30s"

admin:
  default_username: "admin"
  default_password: "${ADMIN_PASSWORD}"
```

## 13. Planung & Tracker

- **Issues + Milestones** —
  [github.com/Jeansy91/priva-sync](https://github.com/Jeansy91/priva-sync).
- **Architektur-Entscheidungen** — [`docs/adr/`](docs/adr/) (system-weit),
  `apps/<context>/docs/adr/` (context-spezifisch). Index:
  [`docs/adr/README.md`](docs/adr/README.md).
- **Bekannte Defekte / Roadmap** — leben als offene Issues bzw. Milestones
  im Tracker; nicht doppelt in dieser Spec geführt.
