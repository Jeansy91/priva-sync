# ─── Web Build Stage ────────────────────────────────────────────────────────
# Node 26 Alpine + pnpm baut @privasync/web inkl. Workspace-Deps (ui, api-client).
FROM node:26-alpine AS web-builder

# Pin pnpm to the lockfile's generation (10.x). Node >=25 no longer ships
# corepack, so pnpm comes from npm instead.
RUN npm install -g pnpm@10.34.9

WORKDIR /repo

# Workspace-Manifeste zuerst für besseres Caching. Die Root-package.json traegt
# die pnpm.overrides (@xmldom/xmldom) -- ohne sie schlaegt --frozen-lockfile
# wegen Lockfile/Manifest-Mismatch fehl.
COPY pnpm-workspace.yaml pnpm-lock.yaml package.json ./
COPY apps/web/package.json        apps/web/package.json
COPY packages/ui/package.json     packages/ui/package.json
COPY packages/api-client/package.json packages/api-client/package.json
COPY packages/i18n/package.json   packages/i18n/package.json

RUN pnpm install --frozen-lockfile

# Quellcode nachziehen und bauen. @privasync/web haengt an ui, api-client und
# i18n -- alle drei Workspace-Pakete muessen im Build-Context liegen.
COPY apps/web       apps/web
COPY packages/ui    packages/ui
COPY packages/api-client packages/api-client
COPY packages/i18n  packages/i18n

RUN pnpm --filter @privasync/web build

# ─── Server Build Stage ─────────────────────────────────────────────────────
FROM golang:1.27-alpine AS server-builder

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /build

# Defaults to "dev" -- the same fallback internal/version.Version documents
# for a local build -- so a manual `docker build` without --build-arg still
# works. The release pipeline passes the real version so
# GET /api/v1/admin/stats reports something other than "dev" and the update
# feed offers the embedded client build, which only matches the real version
# (ADR-0088).
ARG VERSION="dev"

COPY apps/server/go.mod apps/server/go.sum ./
RUN go mod download

COPY apps/server .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-w -s -X privasync/server/internal/version.Version=${VERSION}" \
    -o /bin/server ./cmd/server

# ─── Runtime Stage ──────────────────────────────────────────────────────────
# Alpine (statt scratch), damit Healthcheck via wget funktioniert.
FROM alpine:3.24

# apk upgrade first so base packages (e.g. musl) pick up the latest 3.24
# security patches, not just whatever shipped in the base image tag.
# libxml2 is pulled in by libarchive-tools (bsdtar) and must be >= 2.13.9-r5
# (CVE-2026-11979, -86138, -86139, -86140, ...). The explicit constraint makes
# the build fail loudly instead of shipping a vulnerable libxml2 from a stale
# layer or mirror.
RUN apk upgrade --no-cache && \
    apk add --no-cache "libxml2>=2.13.9-r5" ca-certificates tzdata wget ffmpeg poppler-utils libheif-tools libarchive-tools

COPY --from=server-builder /bin/server /usr/local/bin/server
COPY --from=web-builder    /repo/apps/web/dist /srv/web

ENV PRIVASYNC__SERVER__STATIC_PATH=/srv/web

EXPOSE 8080 8081

# Fixed UID/GID (not the next-free `adduser` default) so operators can chown
# the ./data and ./config bind mounts to a stable numeric ID before first
# start — see docker-compose.yml and ADR-0034.
RUN addgroup -g 10001 -S privasync && \
    adduser -u 10001 -S -G privasync -H -D privasync
USER privasync

ENTRYPOINT ["/usr/local/bin/server"]
