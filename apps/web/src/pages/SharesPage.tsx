import { useState, useEffect, useCallback, useMemo } from 'react'
import { useSearchParams } from 'react-router-dom'
import {
  Share2, Loader2, AlertCircle, X, Eye, Pencil, FilePen, Trash2, Globe, Check, Copy, UserPlus, Search,
} from 'lucide-react'
import {
  Button, Input, Select, DateTimePicker, cn,
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter,
} from '@privasync/ui'
import { apiClient, useAuthStore } from '../store/auth'
import type { PathShareInfo, SyncRoot, UserSummary } from '@privasync/api-client'
import { useTranslation } from '@privasync/i18n'
import { useScrolled } from '../hooks/useScrolled'
import { useSSE } from '../hooks/useSSE'
import { UserPickerField } from '../components/UserPickerField'
import { PageHeader } from '../components/PageHeader'

// One manageable share per path: the user-share aggregate (its recipient
// grants) plus the optional public share with independent read/write links.
// Configured here — the Files view only links in. See ADR-0015.
type ShareGroup = {
  rootID:      string
  rootName:    string
  path:        string
  isDir:       boolean
  aggregateID: string | null
  users:       PathShareInfo[]
  pub:         PathShareInfo | null
}

const keyOf = (rootID: string, path: string) => `${rootID} ${path}`

// Human label for a share location. A root share has path "/", which is
// meaningless on its own — fall back to the sync-root name so a share is never
// shown as a bare "/".
const displayPath = (rootName: string, path: string) => `${rootName}${path === '/' ? '' : path}`

export default function SharesPage() {
  const { t, i18n } = useTranslation()
  const localeTag = i18n.language === 'de' ? 'de-DE' : 'en-US'
  const { scrolled, onScroll } = useScrolled()
  const [roots, setRoots]     = useState<SyncRoot[]>([])
  const [groups, setGroups]   = useState<ShareGroup[]>([])
  const [allUsers, setAllUsers] = useState<UserSummary[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError]     = useState<string | null>(null)
  const [query, setQuery]     = useState('')
  const currentUserId = useAuthStore((s) => s.user?.id)

  const [searchParams, setSearchParams] = useSearchParams()
  const qRoot = searchParams.get('root')
  const qPath = searchParams.get('path')
  const qDir  = searchParams.get('dir') === '1'

  const clearTarget = () => {
    const next = new URLSearchParams(searchParams)
    next.delete('root'); next.delete('path'); next.delete('dir')
    setSearchParams(next, { replace: true })
  }

  const load = useCallback(async () => {
    setError(null)
    try {
      apiClient.users.search('').then((u) => setAllUsers(u ?? [])).catch(() => {})
      const rs: SyncRoot[] = (await apiClient.files.listSyncRoots()) ?? []
      setRoots(rs)
      const maps = await Promise.all(
        rs.map(async (r) => ({ root: r, map: await apiClient.sharesByRoot(r.id) })),
      )
      const next: ShareGroup[] = []
      for (const { root, map } of maps) {
        for (const [path, entries] of Object.entries(map)) {
          const users = entries.filter((e) => e.share_type === 'user')
          const pub = entries.find((e) => e.share_type === 'public') ?? null
          if (users.length === 0 && !pub) continue
          next.push({
            rootID: root.id, rootName: root.name, path,
            isDir: path === '/' || pub?.write_token != null || users.some((u) => u.can_write),
            aggregateID: users[0]?.share_id ?? null,
            users, pub,
          })
        }
      }
      next.sort((a, b) =>
        displayPath(a.rootName, a.path).localeCompare(displayPath(b.rootName, b.path), undefined, { sensitivity: 'base' }))
      setGroups(next)
    } catch {
      setError(t('shares.loadFailed'))
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => { load() }, [load])

  useSSE({
    share_added:   () => { load() },
    share_revoked: () => { load() },
    share_updated: () => { load() },
  }, load)

  // A path navigated in from the Files view that has no share yet gets a
  // synthetic, empty group so it can be configured straight away.
  const visible = useMemo<ShareGroup[]>(() => {
    let list = groups
    if (qRoot && qPath && !groups.some((g) => g.rootID === qRoot && g.path === qPath)) {
      const root = roots.find((r) => r.id === qRoot)
      if (root) {
        list = [
          { rootID: qRoot, rootName: root.name, path: qPath, isDir: qDir, aggregateID: null, users: [], pub: null },
          ...groups,
        ]
      }
    }
    if (qPath) list = list.filter((g) => g.path === qPath && (!qRoot || g.rootID === qRoot))
    const q = query.trim().toLowerCase()
    if (q) {
      list = list.filter((g) =>
        displayPath(g.rootName, g.path).toLowerCase().includes(q)
        || g.users.some((u) => (u.target_username ?? '').toLowerCase().includes(q)))
    }
    return list
  }, [groups, roots, qRoot, qPath, qDir, query])

  // Filter chip shows the root-aware label, never a bare "/".
  const filterLabel = qPath
    ? displayPath(roots.find((r) => r.id === qRoot)?.name ?? '', qPath)
    : ''

  return (
    <div className="flex flex-col h-full">
      <PageHeader
        title={t('shares.title')}
        subtitle={t('shares.subtitle')}
        scrolled={scrolled}
        actions={
          <div className="relative w-full sm:w-64">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              type="search"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={t('shares.searchPlaceholder')}
              aria-label={t('shares.searchPlaceholder')}
              className="pl-8"
            />
          </div>
        }
      />

      <div className="flex-1 overflow-auto" onScroll={onScroll}>
        <div className="max-w-7xl mx-auto p-4 sm:p-6 space-y-4">
          {error && (
            <div className="flex items-center gap-2 rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              <AlertCircle className="h-4 w-4 shrink-0" />
              {error}
            </div>
          )}

          {qPath && (
            <div className="flex items-center gap-2 rounded-md border border-border bg-muted/40 px-3 py-2 text-sm">
              <span className="text-muted-foreground">{t('shares.filterPath')}:</span>
              <code className="font-mono">{filterLabel}</code>
              <button
                type="button"
                onClick={clearTarget}
                className="ml-auto text-muted-foreground hover:text-foreground"
                aria-label={t('shares.filterClear')}
                title={t('shares.filterClear')}
              >
                <X className="h-4 w-4" />
              </button>
            </div>
          )}

          {loading && groups.length === 0 ? (
            <div className="flex items-center justify-center py-20 gap-2 text-muted-foreground">
              <Loader2 className="h-5 w-5 animate-spin" />
              <span className="text-sm">{t('shares.loading')}</span>
            </div>
          ) : visible.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-20 gap-3 text-center">
              <Share2 className="h-12 w-12 text-muted-foreground/40" />
              <div>
                <p className="text-base font-medium">{t('shares.noneTitle')}</p>
                <p className="text-sm text-muted-foreground mt-1">{t('shares.noneHint')}</p>
              </div>
            </div>
          ) : (
            <div className="space-y-3">
              {visible.map((g) => (
                <GroupCard
                  key={keyOf(g.rootID, g.path)}
                  group={g}
                  localeTag={localeTag}
                  allUsers={allUsers}
                  currentUserId={currentUserId}
                  onChanged={load}
                  onError={() => setError(t('shares.loadFailed'))}
                />
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

function GroupCard({
  group, localeTag, allUsers, currentUserId, onChanged, onError,
}: {
  group:         ShareGroup
  localeTag:     string
  allUsers:      UserSummary[]
  currentUserId: string | undefined
  onChanged:     () => void
  onError:       () => void
}) {
  const { t } = useTranslation()
  const [busy, setBusy]   = useState(false)
  // null = closed; { edit: null } = add modal; { edit: grant } = edit modal.
  const [dialog, setDialog] = useState<{ edit: PathShareInfo | null } | null>(null)

  // Recipients that may still be added: everyone except the owner and those
  // already granted. When empty, the add option disappears entirely.
  const excludeIds = [
    ...group.users.map((u) => u.target_user_id).filter((id): id is string => !!id),
    ...(currentUserId ? [currentUserId] : []),
  ]
  const hasAddable = allUsers.some((u) => !excludeIds.includes(u.id))

  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true)
    try { await fn(); onChanged() } catch { onError() } finally { setBusy(false) }
  }

  const removeUser = (u: PathShareInfo) =>
    run(() => apiClient.shares.removeGrant(u.share_id, u.target_user_id!))

  const genLink = (kind: 'read' | 'write', expiresIso: string | null) =>
    run(() => apiClient.shares.createPublicLink(group.rootID, group.path, kind, expiresIso))
  const delLink = (kind: 'read' | 'write') =>
    run(() => apiClient.shares.deletePublicLink(group.rootID, group.path, kind))

  const deleteGroup = () => run(async () => {
    if (group.aggregateID) await apiClient.shares.delete(group.aggregateID)
    if (group.pub) await apiClient.shares.delete(group.pub.id)
  })

  return (
    <div className="rounded-lg border border-border bg-card p-4 space-y-3">
      <div className="flex items-center gap-2">
        <Share2 className="h-4 w-4 text-muted-foreground" />
        <span className="font-medium truncate flex-1">{displayPath(group.rootName, group.path)}</span>
        <button
          type="button"
          disabled={busy}
          onClick={deleteGroup}
          className="rounded-sm p-1 text-muted-foreground hover:text-destructive"
          aria-label={t('shares.delete')}
          title={t('shares.delete')}
        >
          <Trash2 className="h-4 w-4" />
        </button>
      </div>

      {/* People */}
      <div className="space-y-1.5">
        <p className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">{t('files.sharePeopleTitle')}</p>
        {group.users.length === 0 && (
          <p className="text-sm text-muted-foreground">{t('files.sharePeopleEmpty')}</p>
        )}
        {group.users.map((u) => (
          <div key={u.id} className="flex items-center gap-2 text-sm">
            <span className="flex-1 truncate">{u.target_username}</span>
            {u.expires_at && (
              <span className="text-xs text-muted-foreground">{t('shares.expires')} {new Date(u.expires_at).toLocaleDateString(localeTag)}</span>
            )}
            <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
              {u.can_write ? <FilePen className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
              {u.can_write ? t('files.mountInfoWritable') : t('files.mountInfoReadOnly')}
            </span>
            <button
              type="button" disabled={busy} onClick={() => setDialog({ edit: u })}
              className="text-muted-foreground hover:text-foreground"
              aria-label={t('common.edit')} title={t('common.edit')}
            >
              <Pencil className="h-4 w-4" />
            </button>
            <button
              type="button" disabled={busy} onClick={() => removeUser(u)}
              className="text-muted-foreground hover:text-destructive"
              aria-label={t('files.shareRemoveUser')} title={t('files.shareRemoveUser')}
            >
              <Trash2 className="h-4 w-4" />
            </button>
          </div>
        ))}

        {hasAddable && (
          <button
            type="button"
            onClick={() => setDialog({ edit: null })}
            className="inline-flex items-center gap-1.5 text-sm text-primary hover:underline"
          >
            <UserPlus className="h-4 w-4" />
            {t('shares.addPerson')}
          </button>
        )}
      </div>

      {/* Public links */}
      <div className="space-y-2 border-t border-border pt-3">
        <p className="inline-flex items-center gap-1.5 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
          <Globe className="h-3.5 w-3.5" />{t('files.sharePublicTitle')}
        </p>
        {/* Legacy shares carry their limit on the aggregate (expires_at);
            per-link expiry wins when both are present. */}
        <PublicRow
          label={t('files.sharePublicReadLink')}
          pickerId={`pub-expiry-read-${group.rootID}-${group.path}`}
          token={group.pub?.read_token ?? null}
          expiresAt={group.pub?.read_expires_at ?? group.pub?.expires_at ?? null}
          localeTag={localeTag}
          busy={busy}
          onGenerate={(iso) => genLink('read', iso)} onDelete={() => delLink('read')}
        />
        {group.isDir && (
          <PublicRow
            label={t('files.sharePublicWriteLink')}
            pickerId={`pub-expiry-write-${group.rootID}-${group.path}`}
            token={group.pub?.write_token ?? null}
            expiresAt={group.pub?.write_expires_at ?? group.pub?.expires_at ?? null}
            localeTag={localeTag}
            busy={busy}
            onGenerate={(iso) => genLink('write', iso)} onDelete={() => delLink('write')}
          />
        )}
      </div>

      {dialog && (
        <GrantDialog
          group={group}
          localeTag={localeTag}
          edit={dialog.edit}
          excludeIds={excludeIds}
          onClose={() => setDialog(null)}
          onSaved={onChanged}
          onError={onError}
        />
      )}
    </div>
  )
}

// isoToLocalInput converts a stored ISO timestamp to the local
// `YYYY-MM-DDTHH:mm` shape the DateTimePicker round-trips.
function isoToLocalInput(iso: string): string {
  const d = new Date(iso)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

// Add or edit one recipient grant in a modal. In add mode the user is picked;
// in edit mode the user is fixed and only the access right + expiry change.
function GrantDialog({
  group, localeTag, edit, excludeIds, onClose, onSaved, onError,
}: {
  group:      ShareGroup
  localeTag:  string
  edit:       PathShareInfo | null
  excludeIds: string[]
  onClose:    () => void
  onSaved:    () => void
  onError:    () => void
}) {
  const { t } = useTranslation()
  const [picked, setPicked]   = useState<UserSummary | null>(null)
  const [right, setRight]     = useState<'read' | 'write'>(edit?.can_write ? 'write' : 'read')
  const [expires, setExpires] = useState(edit?.expires_at ? isoToLocalInput(edit.expires_at) : '')
  const [busy, setBusy]       = useState(false)

  const save = async () => {
    if (!edit && !picked) return
    setBusy(true)
    try {
      const expiresIso = expires ? new Date(expires).toISOString() : null
      const canWrite = group.isDir && right === 'write'
      if (edit) {
        await apiClient.shares.updateGrant(edit.share_id, edit.target_user_id!, {
          can_write: canWrite, expires_at: expiresIso,
        })
      } else {
        await apiClient.shares.create(group.rootID, {
          path: group.path, share_type: 'user', target_user_id: picked!.id,
          can_write: canWrite, ...(expiresIso ? { expires_at: expiresIso } : {}),
        })
      }
      onSaved(); onClose()
    } catch { onError() } finally { setBusy(false) }
  }

  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{edit ? t('shares.editPersonTitle') : t('shares.addPersonTitle')}</DialogTitle>
          <DialogDescription className="truncate">{displayPath(group.rootName, group.path)}</DialogDescription>
        </DialogHeader>

        <div className="space-y-3">
          {edit
            ? <p className="text-sm font-medium">{edit.target_username}</p>
            : <UserPickerField selected={picked} onSelect={setPicked} onClear={() => setPicked(null)} exclude={excludeIds} />}

          {group.isDir && (
            <div className="space-y-1">
              <label className="block text-xs font-medium text-muted-foreground">{t('shares.accessRight')}</label>
              <Select
                value={right}
                onChange={(v) => setRight(v as 'read' | 'write')}
                options={[
                  { value: 'read', label: t('files.mountInfoReadOnly') },
                  { value: 'write', label: t('files.mountInfoWritable') },
                ]}
              />
            </div>
          )}
          <div className="space-y-1">
            <label className="block text-xs font-medium text-muted-foreground">{t('files.shareExpiresLabel')}</label>
            <DateTimePicker value={expires} onChange={setExpires} locale={localeTag} />
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={busy}>{t('common.cancel')}</Button>
          <Button onClick={save} disabled={busy || (!edit && !picked)}>
            {edit ? t('common.save') : t('files.shareAddUser')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// One public link row. Without a token: pick an optional expiry, then
// generate — the limit is part of the link and cannot be changed afterwards
// (regenerate to change it). With a token: show the link and its expiry.
function PublicRow({
  label, pickerId, token, expiresAt, localeTag, busy, onGenerate, onDelete,
}: {
  label: string; pickerId: string; token: string | null
  expiresAt: string | null; localeTag: string; busy: boolean
  onGenerate: (expiresIso: string | null) => void; onDelete: () => void
}) {
  const { t } = useTranslation()
  const [copied, setCopied] = useState(false)
  const [expires, setExpires] = useState('')

  const generate = () => {
    onGenerate(expires ? new Date(expires).toISOString() : null)
    setExpires('')
  }

  return (
    <div className="space-y-1">
      <div className="flex items-center justify-between">
        <p className="text-xs text-muted-foreground">{label}</p>
        {token && (
          <button type="button" disabled={busy} onClick={onDelete} className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-destructive">
            <X className="h-3.5 w-3.5" />{t('shares.delete')}
          </button>
        )}
      </div>
      {token ? (
        <>
          <CopyableLink link={`${window.location.origin}/s/${token}`} label={label} copied={copied} setCopied={setCopied} />
          {expiresAt && (
            <p className="text-[11px] text-muted-foreground/70">
              {t('shares.expires')} {new Date(expiresAt).toLocaleString(localeTag, { dateStyle: 'medium', timeStyle: 'short' })}
            </p>
          )}
        </>
      ) : (
        <div className="flex items-center gap-2">
          <label htmlFor={pickerId} className="sr-only">{t('files.shareExpiresLabel')}</label>
          <DateTimePicker
            id={pickerId}
            value={expires}
            onChange={setExpires}
            locale={localeTag}
            placeholder={t('files.shareExpiresLabel')}
            className="max-w-xs"
          />
          <Button size="sm" variant="outline" onClick={generate} disabled={busy}>{t('shares.generate')}</Button>
        </div>
      )}
    </div>
  )
}

function CopyableLink({
  link, label, copied, setCopied,
}: { link: string; label: string; copied: boolean; setCopied: (b: boolean) => void }) {
  const copy = async () => {
    await navigator.clipboard.writeText(link)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }
  return (
    <div className="flex h-9 items-center gap-2 rounded-md border border-input bg-muted px-3">
      <code className="text-xs flex-1 truncate">{link}</code>
      <button onClick={copy} className="shrink-0 text-muted-foreground hover:text-foreground" aria-label={label}>
        {copied ? <Check className="h-4 w-4 text-green-500" /> : <Copy className="h-4 w-4" />}
      </button>
    </div>
  )
}
