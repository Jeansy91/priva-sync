import { useEffect, useState } from "react";
import {
  AlertTriangle, CheckCircle, FileWarning, RefreshCw,
  CheckCheck, TriangleAlert, X, AlertOctagon,
} from "lucide-react";
import { Button } from "@privasync/ui";
import { Trans, useTranslation } from "@privasync/i18n";
import { cmd, onSyncConflict, onSyncFailure, type LocalFile, type SyncFailure, type PathWarning } from "../tauri";
import { commandErrorMessage } from "../commandError";
import { useAppStore } from "../store";
import ConflictResolveDialog, { type DesktopConflictTarget } from "./ConflictResolveDialog";

export default function ConflictsTab() {
  const { t } = useTranslation();
  const { syncRoots, pathWarnings, dismissPathWarning } = useAppStore();
  const [conflicts, setConflicts] = useState<LocalFile[]>([]);
  const [failures, setFailures]   = useState<SyncFailure[]>([]);
  const [error, setError]         = useState("");
  const [loading, setLoading]     = useState(true);
  const [resolveTarget, setResolveTarget] = useState<DesktopConflictTarget | null>(null);
  const [retrying, setRetrying]   = useState<string | null>(null);

  async function load() {
    setLoading(true);
    try {
      const [c, f] = await Promise.all([
        cmd.getConflicts(),
        cmd.listSyncFailures(),
      ]);
      setConflicts(c);
      setFailures(f);
      setError("");
    } catch (err) {
      setError(commandErrorMessage(err));
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    load();
    const unlistenC = onSyncConflict(() => load());
    const unlistenF = onSyncFailure(() => load());
    return () => {
      unlistenC.then((fn) => fn());
      unlistenF.then((fn) => fn());
    };
  }, []);

  function openResolve(f: LocalFile) {
    setResolveTarget({ rootId: f.sync_root_id, path: f.path });
  }

  async function handleRetry(f: SyncFailure) {
    const key = `${f.kind}:${f.root_id}:${f.path}`;
    setRetrying(key);
    try {
      await cmd.retrySyncFailure(f.kind, f.root_id, f.path);
      await load();
    } catch (err) {
      setError(commandErrorMessage(err));
    } finally {
      setRetrying(null);
    }
  }

  function rootName(id: string) {
    return syncRoots.find((r) => r.id === id)?.name ?? id;
  }

  // Translate a path-warning code (server/daemon emits codes; the UI localises).
  function warnMessage(w: PathWarning): string {
    switch (w.code) {
      case "path_too_long":  return t("desktop.conflicts.warn.pathTooLong", w.params);
      case "invalid_chars":  return t("desktop.conflicts.warn.invalidChars");
      case "case_collision": return t("desktop.conflicts.warn.caseCollision");
      default:               return w.code;
    }
  }

  function formatNextRetry(ts: number | null): string {
    if (ts == null) return t("desktop.conflicts.nextRetryDash");
    if (ts >= Number.MAX_SAFE_INTEGER / 2) return t("desktop.conflicts.nextRetryManual");
    const diff = ts - Math.floor(Date.now() / 1000);
    if (diff <= 0)   return t("desktop.conflicts.nextRetryNow");
    if (diff < 60)   return t("desktop.conflicts.nextRetrySec",  { n: diff });
    if (diff < 3600) return t("desktop.conflicts.nextRetryMin",  { n: Math.floor(diff / 60) });
    return                  t("desktop.conflicts.nextRetryHour", { n: Math.floor(diff / 3600) });
  }

  const hasIssues = conflicts.length > 0 || pathWarnings.length > 0 || failures.length > 0;

  if (loading) {
    return (
      <div className="flex items-center justify-center py-16">
        <div className="h-6 w-6 animate-spin rounded-full border-4 border-blue-500 border-t-transparent" />
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-semibold text-foreground">
          {t("desktop.conflicts.title")}
          {hasIssues && (
            <span className="ml-2 inline-flex items-center rounded-full bg-red-100 dark:bg-red-900/40
                             px-2 py-0.5 text-xs font-medium text-red-700 dark:text-red-300">
              {conflicts.length + pathWarnings.length}
            </span>
          )}
        </h2>
        <Button variant="outline" size="sm" onClick={load} className="gap-1 text-muted-foreground">
          <RefreshCw className="h-3.5 w-3.5" />
          {t("desktop.common.refresh")}
        </Button>
      </div>

      {error && (
        <p className="rounded-lg bg-red-50 dark:bg-red-950/40 px-3 py-2 text-xs text-red-700 dark:text-red-300">{error}</p>
      )}

      {conflicts.length > 0 && (
        <section className="space-y-2">
          <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">
            {t("desktop.conflicts.sections.conflicts")}
          </p>
          <ul className="space-y-2">
            {conflicts.map((f) => {
              const key = `${f.sync_root_id}:${f.path}`;
              return (
                <li
                  key={key}
                  className="rounded-xl border border-yellow-200 dark:border-yellow-900 bg-yellow-50 dark:bg-yellow-950/40 p-3"
                >
                  <div className="flex items-start gap-3">
                    <FileWarning className="mt-0.5 h-5 w-5 shrink-0 text-yellow-600 dark:text-yellow-400" />
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-sm font-medium text-foreground">
                        {f.path}
                      </p>
                      <p className="text-xs text-muted-foreground">
                        {t("desktop.conflicts.folder")}:{" "}
                        <span className="font-medium text-foreground">
                          {rootName(f.sync_root_id)}
                        </span>
                      </p>
                      <p className="mt-1 text-xs text-yellow-700 dark:text-yellow-300">
                        <Trans
                          i18nKey="desktop.conflicts.copySaved"
                          components={[<code className="rounded-sm bg-yellow-100 dark:bg-yellow-900/40 px-1" />]}
                        />
                      </p>
                    </div>
                    <button
                      onClick={() => openResolve(f)}
                      title={t("desktop.conflicts.markResolved")}
                      className="shrink-0 flex items-center gap-1 rounded-lg
                                 border border-green-300 dark:border-green-800 bg-card px-2 py-1.5
                                 text-xs text-green-700 dark:text-green-300 hover:bg-green-50 dark:hover:bg-green-950/40
                                 disabled:opacity-50"
                    >
                      <CheckCheck className="h-3.5 w-3.5" />
                      {t("desktop.conflicts.resolved")}
                    </button>
                  </div>
                </li>
              );
            })}
          </ul>
        </section>
      )}

      {failures.length > 0 && (
        <section className="space-y-2">
          <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">
            {t("desktop.conflicts.sections.failures")}
          </p>
          <ul className="space-y-2">
            {failures.map((f) => {
              const key = `${f.kind}:${f.root_id}:${f.path}`;
              const isRetrying = retrying === key;
              return (
                <li
                  key={key}
                  className="rounded-xl border border-red-200 dark:border-red-900 bg-red-50 dark:bg-red-950/40 p-3"
                >
                  <div className="flex items-start gap-3">
                    <AlertOctagon className="mt-0.5 h-5 w-5 shrink-0 text-red-600 dark:text-red-400" />
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-sm font-medium text-foreground">
                        {f.path}
                      </p>
                      <p className="text-xs text-muted-foreground">
                        {t("desktop.conflicts.folder")}:{" "}
                        <span className="font-medium text-foreground">
                          {rootName(f.root_id)}
                        </span>
                        {" · "}{t("desktop.conflicts.kind")}:{" "}
                        <span className="font-medium text-foreground">{f.kind}</span>
                        {" · "}{t("desktop.conflicts.attempts")}:{" "}
                        <span className="font-medium text-foreground">{f.attempt_count}</span>
                        {" · "}{t("desktop.conflicts.nextRetry")}:{" "}
                        <span className="font-medium text-foreground">{formatNextRetry(f.next_retry_at)}</span>
                      </p>
                      <p className="mt-1 wrap-break-word text-xs text-red-700 dark:text-red-300">
                        {f.last_error}
                      </p>
                    </div>
                    <button
                      onClick={() => handleRetry(f)}
                      disabled={isRetrying}
                      title={t("desktop.conflicts.retryNow")}
                      className="shrink-0 flex items-center gap-1 rounded-lg
                                 border border-red-300 dark:border-red-800 bg-card px-2 py-1.5
                                 text-xs text-red-700 dark:text-red-300 hover:bg-red-50 dark:hover:bg-red-950/40
                                 disabled:opacity-50"
                    >
                      {isRetrying
                        ? <div className="h-3.5 w-3.5 animate-spin rounded-full border-2
                                          border-red-500 border-t-transparent" />
                        : <RefreshCw className="h-3.5 w-3.5" />}
                      {t("desktop.common.retry")}
                    </button>
                  </div>
                </li>
              );
            })}
          </ul>
        </section>
      )}

      {pathWarnings.length > 0 && (
        <section className="space-y-2">
          <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">
            {t("desktop.conflicts.sections.warnings")}
          </p>
          <ul className="space-y-2">
            {pathWarnings.map((w) => (
              <li
                key={w.path}
                className="rounded-xl border border-orange-200 dark:border-orange-900 bg-orange-50 dark:bg-orange-950/40 p-3"
              >
                <div className="flex items-start gap-3">
                  <TriangleAlert className="mt-0.5 h-5 w-5 shrink-0 text-orange-500" />
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-sm font-medium text-foreground">
                      {w.path}
                    </p>
                    <p className="mt-0.5 text-xs text-orange-700 dark:text-orange-300">{warnMessage(w)}</p>
                    <p className="mt-1 text-xs text-muted-foreground">
                      {w.blocking
                        ? t("desktop.conflicts.warningSkipped")
                        : t("desktop.conflicts.warningSyncedAnyway")}
                    </p>
                    {w.code === "path_too_long" && (
                      <p className="mt-1 text-xs text-muted-foreground">
                        {t("desktop.conflicts.warningLongPathHint")}
                      </p>
                    )}
                  </div>
                  <button
                    onClick={() => dismissPathWarning(w.path)}
                    title={t("desktop.conflicts.warningDismiss")}
                    className="shrink-0 rounded-sm p-1 text-muted-foreground/70 hover:bg-orange-100 dark:hover:bg-orange-900/40
                               hover:text-foreground"
                  >
                    <X className="h-3.5 w-3.5" />
                  </button>
                </div>
              </li>
            ))}
          </ul>
        </section>
      )}

      {!hasIssues && (
        <div className="flex flex-col items-center gap-3 rounded-xl border-2 border-dashed
                        border-border py-12 text-center">
          <CheckCircle className="h-10 w-10 text-green-400" />
          <p className="text-sm font-medium text-muted-foreground">{t("desktop.conflicts.allClearTitle")}</p>
          <p className="text-xs text-muted-foreground/70">{t("desktop.conflicts.allClearSubtitle")}</p>
        </div>
      )}

      <div className="rounded-lg bg-muted/40 p-3 text-xs text-muted-foreground space-y-1">
        <p>
          <AlertTriangle className="mr-1 inline h-3.5 w-3.5 text-yellow-500" />
          <strong>{t("desktop.conflicts.info.conflictTitle")}</strong>
          <Trans
            i18nKey="desktop.conflicts.info.conflictBody"
            components={[<code className="rounded-sm bg-muted px-1" />]}
          />
        </p>
        <p>
          <TriangleAlert className="mr-1 inline h-3.5 w-3.5 text-orange-400" />
          <strong>{t("desktop.conflicts.info.warningTitle")}</strong>
          {t("desktop.conflicts.info.warningBody")}
        </p>
      </div>

      <ConflictResolveDialog
        target={resolveTarget}
        onClose={() => setResolveTarget(null)}
        onResolved={load}
      />
    </div>
  );
}
