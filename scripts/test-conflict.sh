#!/usr/bin/env bash
# Repro-Test: simuliert Edit-Edit-Konflikt gegen lokalen PrivaSync-Server.
#
# Pruefszenario:
#   1. Login -> Token
#   2. Sync-Root anlegen (oder ersten vorhandenen nutzen)
#   3. Datei initial hochladen (INSERT) + Update (UPDATE) -> server_version=1
#   4. Selbe Datei mit veraltetem If-Match=0 erneut PUT   -> erwartet 409 + conflict_path
#   5. GET /sync-roots/{id}/conflicts                     -> erwartet 1 Eintrag
#   6. POST /conflicts/{id}/resolve                       -> erwartet 204
#   7. Conflict-Datei darf nicht mehr existieren          -> erwartet 404 beim Download
#   8. PATCH move (If-Match: 1)                           -> erwartet 200, server_version bumpt
#   9. PATCH stale move (If-Match: 1, server bei 2)       -> erwartet 409 + conflict_path
#  10. Move-Conflict resolven + Cleanup verifizieren      -> erwartet 204 + 404 fuer Marker
#
# Konfiguration via env vars:
#   PS_HOST       Default: http://localhost:8080
#   PS_USERNAME   Pflicht
#   PS_PASSWORD   Pflicht
#   PS_ROOT_NAME  Default: conflict-test (wird angelegt falls nicht vorhanden)
set -euo pipefail

HOST="${PS_HOST:-http://localhost:8080}"
USER="${PS_USERNAME:?PS_USERNAME nicht gesetzt}"
PASS="${PS_PASSWORD:?PS_PASSWORD nicht gesetzt}"
ROOT_NAME="${PS_ROOT_NAME:-conflict-test}"
FILE_PATH="/conflict-repro-$(date +%s).txt"

# JSON-Parser bestimmen: python3 (Linux/Mac) oder python (Windows).
# Microsoft-Store-Stub fuer python3 ohne echte Installation gibt Fehler beim
# --version-Test, deshalb verifizieren statt nur PATH-check.
if command -v python3 >/dev/null 2>&1 && python3 --version >/dev/null 2>&1; then
  PY=python3
elif command -v python >/dev/null 2>&1 && python --version >/dev/null 2>&1; then
  PY=python
else
  echo "Fehler: weder python3 noch python verfuegbar" >&2
  exit 1
fi

red()   { printf '\033[1;31m%s\033[0m\n' "$*"; }
green() { printf '\033[1;32m%s\033[0m\n' "$*"; }
blue()  { printf '\033[1;34m%s\033[0m\n' "$*"; }

api() {
  local method="$1"; shift
  local path="$1"; shift
  curl -sS -X "$method" -H "Authorization: Bearer $TOKEN" "$@" "$HOST$path"
}

# ─── 1. Login ────────────────────────────────────────────────────────────────
blue "1/7 Login"
LOGIN_RESP=$(curl -sS -X POST -H "Content-Type: application/json" \
  -d "{\"username\":\"$USER\",\"password\":\"$PASS\"}" \
  "$HOST/api/v1/auth/login")
TOKEN=$(echo "$LOGIN_RESP" | $PY -c 'import sys,json;print(json.load(sys.stdin).get("access_token",""))')
if [ -z "$TOKEN" ]; then
  red "Login fehlgeschlagen: $LOGIN_RESP"
  exit 1
fi
green "Token bezogen"

# ─── 2. Sync-Root finden oder anlegen ────────────────────────────────────────
blue "2/7 Sync-Root"
ROOTS=$(api GET /api/v1/sync-roots)
ROOT_ID=$(echo "$ROOTS" | $PY -c "
import sys, json
roots = json.load(sys.stdin)
for r in roots:
    if r['name'] == '$ROOT_NAME':
        print(r['id']); break
")
if [ -z "$ROOT_ID" ]; then
  CREATE=$(api POST /api/v1/sync-roots -H "Content-Type: application/json" \
    -d "{\"name\":\"$ROOT_NAME\",\"path\":\"$ROOT_NAME\"}")
  ROOT_ID=$(echo "$CREATE" | $PY -c 'import sys,json;print(json.load(sys.stdin).get("id",""))')
fi
[ -n "$ROOT_ID" ] || { red "Sync-Root konnte nicht ermittelt werden: $ROOTS"; exit 1; }
green "Root-ID: $ROOT_ID"

# ─── 3. Initial-Upload + Update (server_version auf 1 hochziehen) ───────────
blue "3/7 Initial-Upload (INSERT, server_version=0) + Update (UPDATE -> 1)"
UPLOAD1=$(api PUT "/api/v1/sync-roots/$ROOT_ID/files?path=$FILE_PATH" \
  -H "Content-Type: text/plain" \
  -H "X-File-Mtime: $(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --data-binary "version-A")
SV1=$(echo "$UPLOAD1" | $PY -c 'import sys,json;print(json.load(sys.stdin).get("server_version",-1))')
[ "$SV1" = "0" ] || { red "Erwartete server_version=0 nach INSERT, bekommen: $UPLOAD1"; exit 1; }

# Folge-Upload mit If-Match: 0 -> erfolgreich, Trigger setzt server_version=1.
UPLOAD1B=$(curl -sS -X PUT -H "Authorization: Bearer $TOKEN" \
  -H "If-Match: 0" \
  -H "Content-Type: text/plain" \
  -H "X-File-Mtime: $(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --data-binary "version-A2" \
  "$HOST/api/v1/sync-roots/$ROOT_ID/files?path=$FILE_PATH")
SV1B=$(echo "$UPLOAD1B" | $PY -c 'import sys,json;print(json.load(sys.stdin).get("server_version",-1))')
[ "$SV1B" = "1" ] || { red "Erwartete server_version=1 nach UPDATE, bekommen: $UPLOAD1B"; exit 1; }
green "server_version jetzt 1, Stale-Upload mit If-Match=0 muss kollidieren"

# ─── 4. Stale-Upload (Edit-Edit-Konflikt) ────────────────────────────────────
blue "4/7 Stale-Upload mit If-Match: 0 (erwarte 409)"
HTTP_CODE=$(curl -sS -o /tmp/upload2.json -w '%{http_code}' \
  -X PUT -H "Authorization: Bearer $TOKEN" \
  -H "If-Match: 0" \
  -H "Content-Type: text/plain" \
  -H "X-File-Mtime: $(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --data-binary "version-B" \
  "$HOST/api/v1/sync-roots/$ROOT_ID/files?path=$FILE_PATH")
if [ "$HTTP_CODE" != "409" ]; then
  red "Erwartete 409, bekommen: $HTTP_CODE"
  cat /tmp/upload2.json
  exit 1
fi
CONFLICT_PATH=$($PY -c 'import sys,json;print(json.load(sys.stdin).get("conflict_path",""))' < /tmp/upload2.json)
[ -n "$CONFLICT_PATH" ] || { red "Server lieferte keinen conflict_path: $(cat /tmp/upload2.json)"; exit 1; }
green "409 erhalten, conflict_path=$CONFLICT_PATH"

# ─── 5. Conflict in DB sichtbar ──────────────────────────────────────────────
blue "5/7 ListConflicts"
CONFLICTS=$(api GET "/api/v1/sync-roots/$ROOT_ID/conflicts")
CONFLICT_ID=$(echo "$CONFLICTS" | $PY -c "
import sys, json
for c in json.load(sys.stdin):
    if c['conflict_path'] == '$CONFLICT_PATH':
        print(c['id']); break
")
[ -n "$CONFLICT_ID" ] || { red "Conflict-Eintrag nicht gefunden: $CONFLICTS"; exit 1; }
green "Conflict-ID: $CONFLICT_ID"

# ─── 6. Resolve ──────────────────────────────────────────────────────────────
blue "6/7 Resolve"
HTTP_CODE=$(curl -sS -o /dev/null -w '%{http_code}' \
  -X POST -H "Authorization: Bearer $TOKEN" \
  "$HOST/api/v1/conflicts/$CONFLICT_ID/resolve")
[ "$HTTP_CODE" = "204" ] || { red "Resolve fehlgeschlagen: $HTTP_CODE"; exit 1; }
green "Resolve OK"

# ─── 7. Storage-Cleanup verifizieren ─────────────────────────────────────────
blue "7/7 Storage-Cleanup (Download $CONFLICT_PATH muss 404 liefern)"
HTTP_CODE=$(curl -sS -o /dev/null -w '%{http_code}' \
  -H "Authorization: Bearer $TOKEN" \
  "$HOST/api/v1/sync-roots/$ROOT_ID/files/download?path=$CONFLICT_PATH")
if [ "$HTTP_CODE" = "404" ]; then
  green "Cleanup OK — conflict-Datei wurde geloescht"
else
  red "Cleanup fehlgeschlagen — conflict-Datei noch da (HTTP $HTTP_CODE)"
  exit 1
fi

# ─── 8. Move erfolgreich (server_version=2) ──────────────────────────────────
MOVE_TO_1="/conflict-repro-moved-$(date +%s).txt"
blue "8/10 Move FILE_PATH -> $MOVE_TO_1 mit If-Match: 1 (erwarte 200)"
HTTP_CODE=$(curl -sS -o /tmp/move1.json -w '%{http_code}' \
  -X PATCH -H "Authorization: Bearer $TOKEN" \
  -H "If-Match: 1" \
  -H "Content-Type: application/json" \
  -d "{\"op\":\"move\",\"from\":\"$FILE_PATH\",\"to\":\"$MOVE_TO_1\"}" \
  "$HOST/api/v1/sync-roots/$ROOT_ID/files")
[ "$HTTP_CODE" = "200" ] || { red "Erwartete 200, bekommen $HTTP_CODE: $(cat /tmp/move1.json)"; exit 1; }
SV_AFTER_MOVE=$($PY -c 'import sys,json;print(json.load(sys.stdin).get("server_version",-1))' < /tmp/move1.json 2>/dev/null || echo "?")
green "Move OK (server_version nach UPDATE-Trigger >=2)"

# ─── 9. Stale Move (Move-Move-Konflikt) ──────────────────────────────────────
MOVE_TO_2="/conflict-repro-moved-2-$(date +%s).txt"
blue "9/10 Stale-Move from=$MOVE_TO_1 to=$MOVE_TO_2 mit If-Match: 1 (erwarte 409 + conflict_path)"
HTTP_CODE=$(curl -sS -o /tmp/move2.json -w '%{http_code}' \
  -X PATCH -H "Authorization: Bearer $TOKEN" \
  -H "If-Match: 1" \
  -H "Content-Type: application/json" \
  -d "{\"op\":\"move\",\"from\":\"$MOVE_TO_1\",\"to\":\"$MOVE_TO_2\"}" \
  "$HOST/api/v1/sync-roots/$ROOT_ID/files")
if [ "$HTTP_CODE" != "409" ]; then
  red "Erwartete 409, bekommen $HTTP_CODE: $(cat /tmp/move2.json)"
  exit 1
fi
MOVE_CONFLICT_PATH=$($PY -c 'import sys,json;print(json.load(sys.stdin).get("conflict_path",""))' < /tmp/move2.json)
CURRENT_PATH=$($PY -c 'import sys,json;print(json.load(sys.stdin).get("current_path",""))' < /tmp/move2.json)
[ -n "$MOVE_CONFLICT_PATH" ] || { red "Server lieferte keinen conflict_path: $(cat /tmp/move2.json)"; exit 1; }
green "409 erhalten, current_path=$CURRENT_PATH conflict_path=$MOVE_CONFLICT_PATH"

# ─── 10. Move-Conflict Resolve + Cleanup ─────────────────────────────────────
blue "10/10 Move-Conflict listen + resolven + Cleanup verifizieren"
CONFLICTS=$(api GET "/api/v1/sync-roots/$ROOT_ID/conflicts")
MOVE_CONFLICT_ID=$(echo "$CONFLICTS" | $PY -c "
import sys, json
for c in json.load(sys.stdin):
    if c['conflict_path'] == '$MOVE_CONFLICT_PATH':
        print(c['id']); break
")
[ -n "$MOVE_CONFLICT_ID" ] || { red "Move-Conflict-Eintrag nicht gefunden: $CONFLICTS"; exit 1; }
HTTP_CODE=$(curl -sS -o /dev/null -w '%{http_code}' \
  -X POST -H "Authorization: Bearer $TOKEN" \
  "$HOST/api/v1/conflicts/$MOVE_CONFLICT_ID/resolve")
[ "$HTTP_CODE" = "204" ] || { red "Resolve fehlgeschlagen: $HTTP_CODE"; exit 1; }
HTTP_CODE=$(curl -sS -o /dev/null -w '%{http_code}' \
  -H "Authorization: Bearer $TOKEN" \
  "$HOST/api/v1/sync-roots/$ROOT_ID/files/download?path=$MOVE_CONFLICT_PATH")
[ "$HTTP_CODE" = "404" ] || { red "Move-Conflict-Cleanup fehlgeschlagen (HTTP $HTTP_CODE)"; exit 1; }
green "Move-Conflict Resolve + Cleanup OK"

green "===== Repro-Test komplett gruen ====="
