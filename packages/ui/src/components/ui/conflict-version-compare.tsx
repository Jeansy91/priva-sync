import { useState, type ReactNode } from 'react'
import {
  compareVersions,
  formatExactBytes,
  formatHumanBytes,
  formatTimestamp,
} from '../../lib/conflictCompare'

// Both competing versions of a conflict side by side (ADR-0003): exact size and
// modification time down to the second for every file type, differing values
// highlighted, and — when the caller supplies preview URLs — the two images
// next to each other with their pixel dimensions. Headless and i18n-free like
// DiffView: the caller passes already-translated labels.

export interface ConflictVersionSide {
  label: string
  path: string
  icon?: ReactNode
  sizeBytes: number | null
  modifiedAt: string | Date | null
  /** Row label for the date — "Modified" for a file, "Detected" as fallback. */
  modifiedLabel: string
  /**
   * False when the date is a stand-in (e.g. when the conflict was detected)
   * rather than the file's own modification time: it is shown but neither
   * highlighted nor used to call the versions identical.
   */
  modifiedComparable?: boolean
  /** Object or data URL of the image; omitted for non-images. */
  previewSrc?: string
}

export interface ConflictVersionCompareProps {
  current: ConflictVersionSide
  incoming: ConflictVersionSide
  locale: string
  sizeLabel: string
  unknownLabel: string
  /** Shown when size and date match on both sides. */
  identicalLabel: string
}

export function ConflictVersionCompare({
  current,
  incoming,
  locale,
  sizeLabel,
  unknownLabel,
  identicalLabel,
}: ConflictVersionCompareProps) {
  const comparableDate = current.modifiedComparable !== false && incoming.modifiedComparable !== false
  const diff = compareVersions(
    current,
    incoming,
    comparableDate,
  )
  const hasPreview = Boolean(current.previewSrc && incoming.previewSrc)

  return (
    <div>
      <div className="grid grid-cols-2 gap-3">
        {[current, incoming].map((side, i) => (
          <VersionCard
            key={i}
            side={side}
            locale={locale}
            sizeLabel={sizeLabel}
            unknownLabel={unknownLabel}
            sizeDiffers={diff.size}
            modifiedDiffers={diff.modified}
          />
        ))}
      </div>

      {diff.identical && (
        <p className="mt-2 text-xs text-muted-foreground">{identicalLabel}</p>
      )}

      {hasPreview && (
        <div className="mt-3 grid grid-cols-2 gap-3">
          <Preview side={current} />
          <Preview side={incoming} />
        </div>
      )}
    </div>
  )
}

function VersionCard({
  side,
  locale,
  sizeLabel,
  unknownLabel,
  sizeDiffers,
  modifiedDiffers,
}: {
  side: ConflictVersionSide
  locale: string
  sizeLabel: string
  unknownLabel: string
  sizeDiffers: boolean
  modifiedDiffers: boolean
}) {
  const modified = side.modifiedAt === null ? '' : formatTimestamp(side.modifiedAt, locale)
  return (
    <div className="rounded-lg border border-border p-3 space-y-2 min-w-0">
      <div className="flex items-center gap-2">
        {side.icon}
        <span className="text-sm font-medium">{side.label}</span>
      </div>
      <p className="text-xs text-muted-foreground truncate" title={side.path}>{side.path}</p>
      <dl className="text-xs space-y-1">
        <Row label={sizeLabel} highlight={sizeDiffers}>
          {side.sizeBytes === null ? unknownLabel : (
            <>
              {formatExactBytes(side.sizeBytes, locale)}
              <span className="block font-normal text-muted-foreground">
                {formatHumanBytes(side.sizeBytes, locale)}
              </span>
            </>
          )}
        </Row>
        <Row label={side.modifiedLabel} highlight={modifiedDiffers}>
          {modified || unknownLabel}
        </Row>
      </dl>
    </div>
  )
}

function Row({ label, highlight, children }: { label: string; highlight: boolean; children: ReactNode }) {
  return (
    <div className="flex justify-between gap-2">
      <dt className="text-muted-foreground">{label}</dt>
      <dd
        className={
          highlight
            ? 'font-semibold text-right text-amber-700 dark:text-amber-300'
            : 'font-medium text-right'
        }
      >
        {children}
      </dd>
    </div>
  )
}

function Preview({ side }: { side: ConflictVersionSide }) {
  const [dims, setDims] = useState<string | null>(null)
  return (
    <figure className="m-0 min-w-0 rounded-lg border border-border overflow-hidden">
      <div className="flex items-center justify-center bg-muted/30 p-1">
        <img
          src={side.previewSrc}
          alt={side.label}
          className="max-h-56 max-w-full object-contain"
          onLoad={(e) => {
            const img = e.currentTarget
            setDims(`${img.naturalWidth} × ${img.naturalHeight} px`)
          }}
        />
      </div>
      <figcaption className="px-2 py-1 text-xs text-muted-foreground flex justify-between gap-2">
        <span className="truncate">{side.label}</span>
        {dims && <span className="shrink-0">{dims}</span>}
      </figcaption>
    </figure>
  )
}
