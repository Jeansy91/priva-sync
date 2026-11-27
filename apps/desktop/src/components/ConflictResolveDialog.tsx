import { useEffect, useState } from "react";
import { FileClock, FileDiff, FileText, Loader2 } from "lucide-react";
import {
  Button,
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
  DialogClose,
  DiffView,
  ConflictStrategyChooser,
  ConflictVersionCompare,
} from "@privasync/ui";
import { useTranslation } from "@privasync/i18n";
import {
  cmd,
  type ConflictDiff,
  type ConflictPreview,
  type ConflictVersions,
  type DiffLine,
  type ResolveStrategy,
} from "../tauri";
import { commandErrorMessage } from "../commandError";

export interface DesktopConflictTarget {
  rootId: string;
  path:   string;
}

type DiffState =
  | { kind: "hidden" }
  | { kind: "loading" }
  | { kind: "lines"; lines: DiffLine[] }
  | { kind: "binary" }
  | { kind: "failed" };

/**
 * Desktop conflict resolve dialog (ADR-0003). A desktop conflict is local-
 * only: the server version is already on disk and the prior local bytes sit
 * in a `.conflict.<ts>` copy next to it. The three strategies are local file
 * operations performed by the `resolve_conflict` command. An optional text
 * comparison is computed by the `conflict_diff` command.
 */
export default function ConflictResolveDialog({
  target,
  onClose,
  onResolved,
}: {
  target:     DesktopConflictTarget | null;
  onClose:    () => void;
  onResolved: () => void;
}) {
  const { t, i18n } = useTranslation();
  const [resolving, setResolving] = useState<ResolveStrategy | null>(null);
  const [error, setError] = useState("");
  const [diff, setDiff] = useState<DiffState>({ kind: "hidden" });
  const [versions, setVersions] = useState<ConflictVersions | null>(null);
  const [preview, setPreview] = useState<ConflictPreview | null>(null);

  // Size and date of both sides for every file type; images additionally get
  // a side-by-side preview. A failed lookup leaves the dialog usable without.
  useEffect(() => {
    setVersions(null);
    setPreview(null);
    setDiff({ kind: "hidden" });
    if (!target) return;
    let cancelled = false;
    cmd.conflictVersions(target.rootId, target.path)
      .then((v) => { if (!cancelled) setVersions(v); })
      .catch(() => {});
    cmd.conflictPreview(target.rootId, target.path)
      .then((p) => { if (!cancelled) setPreview(p); })
      .catch(() => {});
    return () => { cancelled = true; };
  }, [target]);

  async function handleResolve(strategy: ResolveStrategy) {
    if (!target) return;
    setResolving(strategy);
    setError("");
    try {
      await cmd.resolveConflict(target.rootId, target.path, strategy);
      onResolved();
      onClose();
    } catch (e) {
      setError(commandErrorMessage(e));
    } finally {
      setResolving(null);
    }
  }

  async function handleShowDiff() {
    if (!target) return;
    setDiff({ kind: "loading" });
    try {
      const res: ConflictDiff = await cmd.conflictDiff(target.rootId, target.path);
      setDiff(res.kind === "binary" ? { kind: "binary" } : { kind: "lines", lines: res.lines });
    } catch {
      setDiff({ kind: "failed" });
    }
  }

  return (
    <Dialog open={target !== null} onOpenChange={(open) => { if (!open) onClose(); }}>
      <DialogContent className="max-w-2xl">
        <DialogClose onClose={onClose} />
        <DialogHeader>
          <DialogTitle>{t("desktop.conflicts.resolveDialog.title")}</DialogTitle>
          <DialogDescription>{t("desktop.conflicts.resolveDialog.description")}</DialogDescription>
        </DialogHeader>

        {target && !versions && (
          <p className="text-xs text-muted-foreground truncate" title={target.path}>{target.path}</p>
        )}

        {target && versions && (
          <ConflictVersionCompare
            locale={i18n.language}
            sizeLabel={t("desktop.conflicts.resolveDialog.size")}
            unknownLabel={t("desktop.conflicts.resolveDialog.unknown")}
            identicalLabel={t("desktop.conflicts.resolveDialog.identical")}
            current={{
              label: t("desktop.conflicts.resolveDialog.current"),
              path: target.path,
              icon: <FileText className="h-4 w-4 text-muted-foreground" />,
              sizeBytes: versions.current.size,
              modifiedAt: versions.current.modified_ms === null ? null : new Date(versions.current.modified_ms),
              modifiedLabel: t("desktop.conflicts.resolveDialog.modified"),
              previewSrc: preview?.current,
            }}
            incoming={{
              label: t("desktop.conflicts.resolveDialog.incoming"),
              path: versions.incoming_name,
              icon: <FileClock className="h-4 w-4 text-amber-500" />,
              sizeBytes: versions.incoming.size,
              modifiedAt: versions.incoming.modified_ms === null ? null : new Date(versions.incoming.modified_ms),
              modifiedLabel: t("desktop.conflicts.resolveDialog.modified"),
              previewSrc: preview?.incoming,
            }}
          />
        )}

        {error && (
          <p className="mt-2 rounded-lg bg-red-50 dark:bg-red-950/40 px-3 py-2 text-xs text-red-700 dark:text-red-300">
            {t("desktop.conflicts.resolveDialog.failed")}
          </p>
        )}

        <div className="mt-3">
          <Button
            variant="outline"
            size="sm"
            onClick={() => (diff.kind === "hidden" || diff.kind === "failed" ? handleShowDiff() : setDiff({ kind: "hidden" }))}
            disabled={diff.kind === "loading"}
          >
            {diff.kind === "loading"
              ? <Loader2 className="h-3.5 w-3.5 animate-spin" />
              : <FileDiff className="h-3.5 w-3.5" />}
            {diff.kind === "lines" || diff.kind === "binary"
              ? t("desktop.conflicts.resolveDialog.hideDiff")
              : t("desktop.conflicts.resolveDialog.showDiff")}
          </Button>

          {diff.kind === "binary" && (
            <p className="mt-2 text-xs text-muted-foreground">{t("desktop.conflicts.resolveDialog.diffBinary")}</p>
          )}
          {diff.kind === "failed" && (
            <p className="mt-2 text-xs text-red-700 dark:text-red-300">{t("desktop.conflicts.resolveDialog.diffFailed")}</p>
          )}
          {diff.kind === "lines" && (
            <DiffView
              lines={diff.lines}
              currentLabel={t("desktop.conflicts.resolveDialog.diffCurrentLabel")}
              incomingLabel={t("desktop.conflicts.resolveDialog.diffIncomingLabel")}
            />
          )}
        </div>

        <div className="mt-4">
          <ConflictStrategyChooser
            disabled={resolving !== null}
            onSelect={(key) => handleResolve(key as ResolveStrategy)}
            options={[
              {
                key:   "keep_current",
                label: t("desktop.conflicts.resolveDialog.keepCurrent"),
                hint:  t("desktop.conflicts.resolveDialog.keepCurrentHint"),
                busy:  resolving === "keep_current",
              },
              {
                key:   "keep_conflict",
                label: t("desktop.conflicts.resolveDialog.keepConflict"),
                hint:  t("desktop.conflicts.resolveDialog.keepConflictHint"),
                busy:  resolving === "keep_conflict",
              },
              {
                key:   "keep_both",
                label: t("desktop.conflicts.resolveDialog.keepBoth"),
                hint:  t("desktop.conflicts.resolveDialog.keepBothHint"),
                busy:  resolving === "keep_both",
              },
            ]}
          />
        </div>

        <DialogFooter>
          <Button variant="outline" size="sm" onClick={onClose} disabled={resolving !== null}>
            {t("desktop.conflicts.resolveDialog.cancel")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

