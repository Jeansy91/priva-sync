import { useEffect, useRef, useState } from "react";
import {
  ArrowUp, ArrowDown,
  CheckCircle, Loader2, AlertTriangle, WifiOff, FolderX, LogIn,
} from "lucide-react";
import { useTranslation } from "@privasync/i18n";
import { onTransferSpeed } from "../tauri";
import { useAppStore } from "../store";

function formatRate(bps: number): string {
  if (bps < 1)            return "0 B/s";
  if (bps < 1024)         return `${Math.round(bps)} B/s`;
  if (bps < 1024 * 1024)  return `${(bps / 1024).toFixed(1)} KB/s`;
  if (bps < 1024 ** 3)    return `${(bps / (1024 * 1024)).toFixed(1)} MB/s`;
  return `${(bps / (1024 ** 3)).toFixed(2)} GB/s`;
}

const statusMeta = {
  idle:            { icon: CheckCircle,   color: "text-green-600 dark:text-green-400",  key: "idle" },
  syncing:         { icon: Loader2,       color: "text-blue-600 dark:text-blue-400",   key: "syncing" },
  conflict:        { icon: AlertTriangle, color: "text-yellow-600 dark:text-yellow-400", key: "conflict" },
  offline:         { icon: WifiOff,       color: "text-muted-foreground/70",   key: "offline" },
  empty:           { icon: FolderX,       color: "text-muted-foreground/70",   key: "empty" },
  unauthenticated: { icon: LogIn,         color: "text-muted-foreground/70",   key: "unauthenticated" },
} as const;

// Smoothing factor for the per-second deltas. Lower = smoother but laggier.
// 0.3 reaches ~95 % of a step change after ~9 samples (~9 seconds), which
// damps the typical TCP-burst noise without making the display feel stale.
const EMA_ALPHA = 0.3;

// How many consecutive empty samples mean the transfer really stopped.
// Snapping to zero on a single one made the readout useless: the sampler ticks
// once a second regardless of transfer size, so an empty second in the middle
// of a large transfer is normal — and resetting the average on each of them
// left the number bouncing between 0 and a fraction of the true rate.
const ZERO_SAMPLES_UNTIL_IDLE = 3;

/** Per-direction smoothing state. Mutated in place by [`advance`]. */
export interface RateSmoother {
  ema:     number;
  zeroRun: number;
}

export function newSmoother(): RateSmoother {
  return { ema: 0, zeroRun: 0 };
}

/** Folds one sampler tick into `s` and returns the rate to display. */
export function advance(s: RateSmoother, raw: number): number {
  s.zeroRun = raw === 0 ? s.zeroRun + 1 : 0;
  s.ema = s.zeroRun >= ZERO_SAMPLES_UNTIL_IDLE
    ? 0
    : EMA_ALPHA * raw + (1 - EMA_ALPHA) * s.ema;
  return s.ema;
}

export default function TransferSpeedBar() {
  const { t } = useTranslation();
  const { status } = useAppStore();
  const [speed, setSpeed] = useState({ up: 0, down: 0 });
  const upRef   = useRef(newSmoother());
  const downRef = useRef(newSmoother());

  useEffect(() => {
    const unlisten = onTransferSpeed((raw) => {
      setSpeed({
        up:   advance(upRef.current,   raw.upload_bps),
        down: advance(downRef.current, raw.download_bps),
      });
    });
    return () => { unlisten.then((fn) => fn()); };
  }, []);

  const effectiveStatus = status?.status ?? "idle";
  const meta = statusMeta[effectiveStatus];
  const Icon = meta.icon;
  const label = t(`desktop.status.${meta.key}`);

  return (
    <div className="flex items-center justify-between gap-4 border-t border-border
                    bg-card px-4 py-1.5 text-[11px] tabular-nums text-muted-foreground">
      <div className="flex items-center gap-1.5">
        <Icon
          className={`h-3.5 w-3.5 ${meta.color} ${effectiveStatus === "syncing" ? "animate-spin" : ""}`}
        />
        <span className={meta.color + " font-medium"}>{label}</span>
        {(status?.pending_count ?? 0) > 0 && (
          <span className="text-muted-foreground/70">
            {t("desktop.status.pending", { count: status!.pending_count })}
          </span>
        )}
      </div>

      <div className="flex items-center gap-4">
        <div className={`flex items-center gap-1 ${speed.up > 0 ? "text-blue-600 dark:text-blue-400" : ""}`}>
          <ArrowUp className="h-3 w-3" />
          {formatRate(speed.up)}
        </div>
        <div className={`flex items-center gap-1 ${speed.down > 0 ? "text-green-600 dark:text-green-400" : ""}`}>
          <ArrowDown className="h-3 w-3" />
          {formatRate(speed.down)}
        </div>
      </div>
    </div>
  );
}
