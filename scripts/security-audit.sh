#!/usr/bin/env bash
# PrivaSync Security Audit Script
# Wird vor jedem Release manuell ausgeführt.
# Benötigt: gosec, cargo-audit, pnpm, semgrep, trivy, gitleaks, docker (ZAP)
set -euo pipefail

REPORT_FILE="security-audit-report-$(git describe --tags --always 2>/dev/null || date +%Y%m%d-%H%M%S).md"
FAILURES=0

log() { echo -e "\n\033[1;34m=== $* ===\033[0m"; }
fail() { echo -e "\033[1;31m[FAIL] $*\033[0m"; FAILURES=$((FAILURES + 1)); }
ok()   { echo -e "\033[1;32m[ OK ] $*\033[0m"; }

{
  echo "# PrivaSync Security Audit Report"
  echo "**Datum:** $(date -u '+%Y-%m-%d %H:%M UTC')"
  echo ""
} > "$REPORT_FILE"

# ─── 1. SAST: Go ──────────────────────────────────────────────────────────────
log "1/8 gosec (Go SAST)"
if command -v gosec &>/dev/null; then
  if gosec -fmt=json ./apps/server/... > /tmp/gosec.json 2>&1; then
    ok "gosec – keine kritischen Findings"
    echo "## gosec: Keine kritischen Findings" >> "$REPORT_FILE"
  else
    fail "gosec – Findings gefunden (siehe /tmp/gosec.json)"
    echo "## gosec: FINDINGS – siehe /tmp/gosec.json" >> "$REPORT_FILE"
  fi
else
  echo "[SKIP] gosec nicht installiert" && echo "## gosec: nicht installiert" >> "$REPORT_FILE"
fi

# ─── 2. SAST: Rust ────────────────────────────────────────────────────────────
log "2/8 cargo audit (Rust CVEs)"
if command -v cargo-audit &>/dev/null; then
  if (cd apps/desktop/src-tauri && cargo audit); then
    ok "cargo audit – keine bekannten CVEs"
  else
    fail "cargo audit – CVEs gefunden"
    FAILURES=$((FAILURES + 1))
  fi
else
  echo "[SKIP] cargo-audit nicht installiert"
fi

# ─── 3. Node Dependencies ─────────────────────────────────────────────────────
log "3/8 pnpm audit (Node CVEs)"
if pnpm audit --audit-level=high; then
  ok "pnpm audit – keine high/critical CVEs"
else
  fail "pnpm audit – CVEs gefunden"
fi

# ─── 4. Semgrep ───────────────────────────────────────────────────────────────
log "4/8 semgrep (OWASP Top 10)"
if command -v semgrep &>/dev/null; then
  if semgrep --config=p/owasp-top-ten --error .; then
    ok "semgrep – keine OWASP-Findings"
  else
    fail "semgrep – Findings gefunden"
  fi
else
  echo "[SKIP] semgrep nicht installiert (pip install semgrep)"
fi

# ─── 5. Trivy ─────────────────────────────────────────────────────────────────
log "5/8 trivy (Container CVEs)"
if command -v trivy &>/dev/null; then
  if trivy image --exit-code 1 --severity HIGH,CRITICAL privasync-server:latest; then
    ok "trivy – keine HIGH/CRITICAL CVEs im Image"
  else
    fail "trivy – kritische CVEs im Docker-Image"
  fi
else
  echo "[SKIP] trivy nicht installiert"
fi

# ─── 6. Secret Scan ───────────────────────────────────────────────────────────
log "6/8 gitleaks (Secret Scan)"
if command -v gitleaks &>/dev/null; then
  if gitleaks detect --source . --no-git; then
    ok "gitleaks – keine Secrets gefunden"
  else
    fail "gitleaks – Secrets im Repository gefunden!"
  fi
else
  echo "[SKIP] gitleaks nicht installiert"
fi

# ─── 7. DAST: OWASP ZAP ───────────────────────────────────────────────────────
log "7/8 OWASP ZAP (DAST gegen lokale Instanz)"
ZAP_TARGET="http://localhost:8080"
if ! command -v docker &>/dev/null; then
  echo "[SKIP] docker nicht installiert (ZAP läuft als owasp/zap2docker-stable Image)"
elif ! curl -sf -o /dev/null "$ZAP_TARGET"; then
  echo "[SKIP] $ZAP_TARGET nicht erreichbar – vorher 'make dev-server' starten"
else
  # --network host shares the Docker host network (Linux only); on Mac/Windows
  # switch ZAP_TARGET to host.docker.internal instead.
  if docker run --rm --network host -v "$(pwd):/zap/wrk:rw" owasp/zap2docker-stable \
      zap-baseline.py -t "$ZAP_TARGET" -r zap-report.html -I; then
    ok "ZAP – keine High-Risk-Findings"
  else
    fail "ZAP – Findings gegen $ZAP_TARGET (siehe zap-report.html)"
  fi
fi

# ─── 8. KI-Review: Claude Code ────────────────────────────────────────────────
log "8/8 KI-Review (Claude Code)"
LAST_TAG=$(git describe --tags --abbrev=0 2>/dev/null || git rev-list --max-parents=0 HEAD)
DIFF=$(git diff "${LAST_TAG}...HEAD" -- apps packages 2>/dev/null)

if [ -z "$DIFF" ]; then
  echo "[SKIP] kein Diff seit $LAST_TAG"
  echo "## KI-Review: kein Diff seit $LAST_TAG" >> "$REPORT_FILE"
elif ! command -v claude &>/dev/null; then
  echo "[SKIP] claude CLI nicht installiert"
  echo "## KI-Review: claude CLI nicht installiert" >> "$REPORT_FILE"
else
  KI_PROMPT="Du bist Security-Reviewer. Pruefe den folgenden Git-Diff (seit ${LAST_TAG}) auf: Auth/Authz-Luecken, Path Traversal, Injection, fehlerhafte Kryptographie, fehlende Input-Validierung, Datenlecks in Logs/Fehlermeldungen. Liste jeden Fund mit Datei, Zeile, Schweregrad (kritisch/hoch/mittel/niedrig) und Begruendung. Schliesse mit exakt einer Zeile 'SEVERITY: <kritisch|hoch|mittel|niedrig|keine>' fuer den hoechsten gefundenen Schweregrad."

  if KI_OUTPUT=$(echo "$DIFF" | claude -p "$KI_PROMPT" --output-format text 2>&1); then
    {
      echo "## KI-Review (seit ${LAST_TAG})"
      echo '```'
      echo "$KI_OUTPUT"
      echo '```'
    } >> "$REPORT_FILE"

    if echo "$KI_OUTPUT" | grep -qiE '^SEVERITY: *(kritisch|hoch)'; then
      fail "KI-Review – kritische/hohe Findings (siehe $REPORT_FILE)"
    else
      ok "KI-Review – keine kritischen/hohen Findings"
    fi
  else
    fail "KI-Review – claude CLI fehlgeschlagen (siehe $REPORT_FILE)"
    echo "## KI-Review: claude CLI fehlgeschlagen – $KI_OUTPUT" >> "$REPORT_FILE"
  fi
fi

# ─── Ergebnis ─────────────────────────────────────────────────────────────────
echo ""
echo "Report gespeichert: $REPORT_FILE"
if [ "$FAILURES" -gt 0 ]; then
  echo -e "\033[1;31mAUDIT FEHLGESCHLAGEN: $FAILURES Prüfung(en) nicht bestanden.\033[0m"
  echo "Release sollte NICHT erfolgen, bis alle Findings behoben sind."
  exit 1
else
  echo -e "\033[1;32mAUDIT BESTANDEN.\033[0m"
fi
