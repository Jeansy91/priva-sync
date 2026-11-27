import { useEffect, useState } from 'react'
import { Download, Eye, Files, FilePen, Folder, Move, Share2, Trash2 } from 'lucide-react'
import { useTranslation } from '@privasync/i18n'
import type { AccessUser, FileEntry, ShareAccess, SharedMount, UserSummaryWithAvatar } from '@privasync/api-client'
import { apiClient } from '../../store/auth'
import { formatBytes, formatDate } from './helpers'
import { mimeForName, previewKindFor } from './previewKind'
import { FileDetailPreview } from './FileDetailPreview'
import { MountAccessIcon } from './MountAccessIcon'
import { UserAvatar } from '../../components/layout/AppShell'

// ADR-0012: Mount is a Decoration on either file- or dir-selection,
// not a separate kind. Layout follows the underlying kind; mount-
// specific rows (giver, access, expiry) are appended at the end when
// `mount` is set.
export type DetailSelection =
  | { kind: 'file'; file: FileEntry; mount?: SharedMount }
  | {
      kind:        'dir'
      name:        string
      path:        string
      itemCount:   number
      totalSize:   number
      latestMtime: string | null
      mount?:      SharedMount
    }

type Props = {
  rootID?:     string
  rootName?:   string
  // True while browsing inside a Shared Mount: rootID is then the share
  // facade id, so a plain in-mount file previews through the mount endpoint
  // (its sub-path being the file's own path). See ADR-0012.
  mountMode?:  boolean
  selection:   DetailSelection | null
  owner?:      UserSummaryWithAvatar | null
  multiCount?: number
  bulk?:       { onDelete: () => void; onMove: () => void; onDownload: () => void; onClear: () => void }
  onShare:     () => void
  onDownload:  () => void
  onPreview:   () => void
}

// Quiet outline style shared by the panel's action buttons (share, download,
// bulk actions). Deliberately flatter than the shared Button component so the
// panel keeps its compact sidebar look.
const actionButtonCls =
  'inline-flex items-center justify-center gap-2 rounded-sm border border-border px-3 py-1.5 text-sm text-foreground hover:bg-accent'

function basename(path: string): string {
  const idx = path.lastIndexOf('/')
  return idx < 0 ? path : path.slice(idx + 1)
}

function extensionOf(name: string): string {
  const dot = name.lastIndexOf('.')
  if (dot <= 0 || dot === name.length - 1) return ''
  return name.slice(dot + 1).toUpperCase()
}

// Joins the sync-root name with an in-root path to produce a user-facing
// breadcrumb-ish path like `Photos/2024/holiday.jpg`. Falls back to the raw
// path when no root name is known.
function fullPath(rootName: string | undefined, p: string): string {
  if (!rootName) return p
  if (p === '/' || p === '') return rootName + '/'
  return rootName + p
}

export function FileDetailPanel({
  rootID, rootName, mountMode = false, selection, owner = null, multiCount = 0, bulk, onShare, onDownload, onPreview,
}: Props) {
  const { t, i18n } = useTranslation()

  if (multiCount > 1) {
    return (
      <aside
        aria-label={t('files.detail.title')}
        className="flex h-full w-full flex-col border-l border-border bg-background"
      >
        <div
          data-testid="detail-multi"
          className="flex flex-col gap-3 overflow-y-auto p-4 text-sm"
        >
          <div className="flex aspect-square w-full items-center justify-center rounded-sm border border-border bg-muted/50">
            <Files className="h-16 w-16 text-blue-500" aria-hidden />
          </div>
          <h2 className="text-center text-base font-semibold text-foreground">
            {t('files.selectionCount', { count: multiCount })}
          </h2>
          {bulk && (
            <div className="mt-2 flex flex-col gap-2">
              <button
                type="button"
                onClick={bulk.onDownload}
                className={actionButtonCls}
              >
                <Download size={14} />
                {t('files.bulkDownload')}
              </button>
              <button
                type="button"
                onClick={bulk.onMove}
                className={actionButtonCls}
              >
                <Move size={14} />
                {t('files.bulkMove')}
              </button>
              <button
                type="button"
                onClick={bulk.onDelete}
                className="inline-flex items-center justify-center gap-2 rounded-sm border border-destructive/40 bg-destructive/10 px-3 py-1.5 text-sm text-destructive hover:bg-destructive/20"
              >
                <Trash2 size={14} />
                {t('files.bulkDelete')}
              </button>
              <button
                type="button"
                onClick={bulk.onClear}
                className="mt-1 inline-flex items-center justify-center gap-2 rounded-sm px-3 py-1.5 text-sm text-muted-foreground hover:bg-accent hover:text-accent-foreground"
              >
                {t('files.bulkClearSelection')}
              </button>
            </div>
          )}
        </div>
      </aside>
    )
  }

  if (!selection) {
    return (
      <aside
        aria-label={t('files.detail.title')}
        className="flex h-full w-full flex-col border-l border-border bg-background p-4 text-sm text-muted-foreground"
      >
        <p>{t('files.detail.emptyHint')}</p>
      </aside>
    )
  }

  const name = selection.kind === 'file' ? basename(selection.file.path) : selection.name
  const isMount = selection.mount !== undefined
  // Owner-Pille spiegelt den tatsächlichen Eigentümer der Daten:
  //  - eigene Datei/Subdir/Sync-Root → owner-Prop (Self)
  //  - Mount jeglicher Art           → mount.owner (Geber)
  // ADR-0012 + ADR-0010.
  const ownerToShow = selection.mount?.owner ?? owner

  return (
    <aside
      aria-label={t('files.detail.title')}
      className="flex h-full w-full flex-col border-l border-border bg-background"
    >
      <div className="flex flex-col gap-3 overflow-y-auto p-4 text-sm">
        {selection.kind === 'file' ? (
          <FileBody
            rootID={rootID}
            rootName={rootName}
            mountMode={mountMode}
            file={selection.file}
            name={name}
            onPreview={onPreview}
            mountID={selection.mount?.is_file ? selection.mount.id : undefined}
          />
        ) : (
          <DirBody dir={selection} rootName={rootName} locale={i18n.language} />
        )}

        {ownerToShow && <OwnerRow owner={ownerToShow} label={t('files.detail.labelOwner')} />}

        {/* ADR-0013: only when last_modifier diverges from the owner. */}
        {selection.kind === 'file'
          && selection.file.last_modifier
          && selection.file.last_modifier.id !== ownerToShow?.id && (
            <OwnerRow
              owner={selection.file.last_modifier}
              label={t('files.detail.labelLastModifier')}
            />
          )}

        {/* Who can reach this item — recipients of the nearest ancestor share,
            split write/read (ADR-0015). Own entries only; the endpoint is
            owner-scoped. */}
        {rootID && !isMount && (
          <AccessSection
            rootID={rootID}
            path={selection.kind === 'file' ? selection.file.path : selection.path}
          />
        )}

        {/* Mount-spezifische Daten-Reihen — am Ende, sobald sie sinnvoll sind. */}
        {selection.mount && (
          <MountFacets mount={selection.mount} locale={i18n.language} />
        )}

        <div className="mt-2 flex flex-wrap gap-2">
          {/* Teilen: nur eigene Einträge (Mounts kann der Empfänger nicht weitergeben). */}
          {!isMount && (
            <button
              type="button"
              onClick={onShare}
              className={actionButtonCls}
            >
              <Share2 size={14} />
              {t('files.detail.share')}
            </button>
          )}
          {/* Herunterladen: Eltern dispatchen file vs. dir vs. mount (ZIP/Single). */}
          <button
            type="button"
            onClick={onDownload}
            className={actionButtonCls}
          >
            <Download size={14} />
            {t('files.detail.download')}
          </button>
        </div>
      </div>
    </aside>
  )
}

// Fetches and renders the access list for a path: writer + reader avatars,
// inherited from ancestor shares. Renders nothing while loading or when no one
// but the owner has access. See ADR-0015.
function AccessSection({ rootID, path }: { rootID: string; path: string }) {
  const { t } = useTranslation()
  const [access, setAccess] = useState<ShareAccess | null>(null)
  useEffect(() => {
    let cancelled = false
    apiClient.shares.access(rootID, path)
      .then((a) => { if (!cancelled) setAccess(a) })
      .catch(() => { if (!cancelled) setAccess(null) })
    return () => { cancelled = true }
  }, [rootID, path])

  if (!access || (access.writers.length === 0 && access.readers.length === 0)) return null
  return (
    <div className="flex flex-col gap-1.5">
      <span className="text-xs uppercase tracking-wide text-muted-foreground">
        {t('files.detail.accessTitle')}
      </span>
      {access.writers.length > 0 && (
        <AccessRow label={t('files.detail.accessWriters')} users={access.writers} writable />
      )}
      {access.readers.length > 0 && (
        <AccessRow label={t('files.detail.accessReaders')} users={access.readers} />
      )}
    </div>
  )
}

function AccessRow({ label, users, writable = false }: { label: string; users: AccessUser[]; writable?: boolean }) {
  const Icon = writable ? FilePen : Eye
  return (
    <div className="flex items-center gap-2" aria-label={label}>
      <Icon className="h-3.5 w-3.5 shrink-0 text-muted-foreground/70" aria-hidden />
      <div className="flex flex-wrap items-center gap-1.5">
        {users.map((u) => (
          <span
            key={u.id}
            className="inline-flex items-center gap-1 rounded-full border border-border bg-muted/50 py-0.5 pl-0.5 pr-2 text-xs text-foreground"
            title={u.username}
          >
            <UserAvatar user={{ id: u.id, username: u.username, avatar_url: u.avatar_url }} size={18} />
            <span className="truncate">{u.username}</span>
          </span>
        ))}
      </div>
    </div>
  )
}

function MountFacets({ mount, locale }: { mount: SharedMount; locale: string }) {
  const { t } = useTranslation()
  return (
    <>
      <DetailRow label={t('files.detail.labelMountAccess')}>
        <MountAccessIcon canWrite={mount.can_write} withLabel />
      </DetailRow>
      {/* Only show the expiry row when one is set — an unset expiry means
          "never" and adds no information. */}
      {mount.expires_at && (
        <DetailRow label={t('files.detail.labelMountExpires')}>
          {new Date(mount.expires_at).toLocaleString(locale)}
        </DetailRow>
      )}
    </>
  )
}

function FileBody({
  rootID, rootName, mountMode, file, name, onPreview, mountID,
}: { rootID?: string; rootName?: string; mountMode?: boolean; file: FileEntry; name: string; onPreview: () => void; mountID?: string }) {
  const { t, i18n } = useTranslation()
  const mime = mimeForName(name) ?? t('files.detail.mimeUnknown')
  const hasPreview = previewKindFor(name) !== null
  const ext = extensionOf(name)
  const kindLabel = ext
    ? t('files.detail.kindFileExt', { ext })
    : t('files.detail.kindFile')
  return (
    <>
      {/* File-mounts preview through the mount endpoint at sub-path "/". A
          plain file browsed inside a dir-mount (mountMode) routes through the
          same endpoint, but with the file's own path and the share id (rootID)
          as the mount id. Own files use the sync-root endpoint. */}
      {mountID ? (
        <FileDetailPreview
          mountID={mountID}
          path="/"
          name={name}
          onClick={hasPreview ? onPreview : undefined}
        />
      ) : mountMode && rootID ? (
        <FileDetailPreview
          mountID={rootID}
          path={file.path}
          name={name}
          onClick={hasPreview ? onPreview : undefined}
        />
      ) : rootID ? (
        <FileDetailPreview
          rootID={rootID}
          path={file.path}
          name={name}
          onClick={hasPreview ? onPreview : undefined}
        />
      ) : null}
      <h2
        className="break-all text-center text-base font-semibold text-foreground"
        title={name}
      >
        {name}
      </h2>
      <DetailRow label={t('files.detail.labelKind')}>{kindLabel}</DetailRow>
      <DetailRow label={t('files.detail.labelSize')}>{formatBytes(file.size)}</DetailRow>
      <DetailRow label={t('files.detail.labelMtime')}>{formatDate(file.mtime, i18n.language)}</DetailRow>
      <DetailRow label={t('files.detail.labelUpdatedAt')}>{formatDate(file.updated_at, i18n.language)}</DetailRow>
      <DetailRow label={t('files.detail.labelPath')}>
        <span className="break-all">{fullPath(rootName, file.path)}</span>
      </DetailRow>
      <DetailRow label={t('files.detail.labelMime')}>{mime}</DetailRow>
    </>
  )
}

function DirBody({
  dir, rootName, locale,
}: { dir: Extract<DetailSelection, { kind: 'dir' }>; rootName?: string; locale: string }) {
  const { t } = useTranslation()
  return (
    <>
      <div className="flex aspect-square w-full items-center justify-center rounded-sm border border-border bg-muted/50">
        <Folder className="h-16 w-16 text-blue-500" />
      </div>
      <h2
        className="break-all text-center text-base font-semibold text-foreground"
        title={dir.name}
      >
        {dir.name}
      </h2>
      <DetailRow label={t('files.detail.labelKind')}>{t('files.detail.kindDir')}</DetailRow>
      <DetailRow label={t('files.detail.labelItemCount')}>{dir.itemCount.toString()}</DetailRow>
      <DetailRow label={t('files.detail.labelSize')}>{formatBytes(dir.totalSize)}</DetailRow>
      {dir.latestMtime && (
        <DetailRow label={t('files.detail.labelMtime')}>{formatDate(dir.latestMtime, locale)}</DetailRow>
      )}
      <DetailRow label={t('files.detail.labelPath')}>
        <span className="break-all">{fullPath(rootName, dir.path)}</span>
      </DetailRow>
    </>
  )
}

function OwnerRow({ owner, label }: { owner: UserSummaryWithAvatar; label: string }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="text-xs uppercase tracking-wide text-muted-foreground">{label}</span>
      <span
        aria-label={label}
        className="inline-flex w-fit items-center gap-2 rounded-full border border-border bg-muted/50 py-0.5 pl-0.5 pr-3 text-sm text-foreground"
      >
        <UserAvatar user={owner} size={24} />
        <span className="truncate">{owner.username}</span>
      </span>
    </div>
  )
}

function DetailRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="text-xs uppercase tracking-wide text-muted-foreground">{label}</span>
      <span aria-label={label} className="text-foreground">{children}</span>
    </div>
  )
}
