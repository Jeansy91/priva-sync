import { useCallback, useEffect, useState } from 'react'
import { ChevronRight, Folder, FolderPlus, Loader2 } from 'lucide-react'
import { cn, Button, Input } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import type { FileEntry } from '@privasync/api-client'
import { apiClient } from '../../store/auth'
import { directSubdirs } from './dirNodes'

type Props = {
  rootID:   string
  rootName: string
  // Currently selected target path (absolute, '/'-rooted).
  value:        string
  onChange:     (path: string) => void
  // Paths that are disabled — typically the sources of the planned move, so
  // the user cannot drop them onto themselves.
  disabledPaths?: Set<string>
  // Fires after a new folder was successfully created via the inline form.
  // Allows the parent surface (e.g. the underlying FilesPage) to refresh its
  // own listing if the new folder sits in the directory it is showing.
  onFolderCreated?: (newPath: string) => void
}

function ancestorPaths(path: string): string[] {
  const parts = path.split('/').filter(Boolean)
  const out: string[] = ['/']
  for (let i = 0; i < parts.length - 1; i++) {
    out.push('/' + parts.slice(0, i + 1).join('/'))
  }
  return out
}

export function FolderTreePicker({ rootID, rootName, value, onChange, disabledPaths, onFolderCreated }: Props) {
  const { t } = useTranslation()
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set(['/']))
  const [files,    setFiles]    = useState<Map<string, FileEntry[]>>(new Map())
  const [loading,  setLoading]  = useState<Set<string>>(new Set())
  // Path under which the inline "new folder" form is currently open.
  const [creatingUnder, setCreatingUnder] = useState<string | null>(null)
  const [newName,    setNewName]      = useState('')
  const [createError, setCreateError] = useState<string | null>(null)
  const [createBusy,  setCreateBusy]  = useState(false)

  const fetchInto = useCallback(async (path: string) => {
    setLoading((prev) => new Set(prev).add(path))
    try {
      const res = await apiClient.files.list(rootID, path)
      setFiles((prev) => new Map(prev).set(path, res.files ?? []))
    } catch {
      setFiles((prev) => new Map(prev).set(path, []))
    } finally {
      setLoading((prev) => {
        const next = new Set(prev)
        next.delete(path)
        return next
      })
    }
  }, [rootID])

  const ensureLoaded = useCallback((path: string) => {
    if (files.has(path) || loading.has(path)) return
    void fetchInto(path)
  }, [files, loading, fetchInto])

  // Preload current value's ancestors so the tree opens at the selected node.
  useEffect(() => {
    const paths = ancestorPaths(value || '/')
    setExpanded((prev) => {
      const next = new Set(prev)
      for (const p of paths) next.add(p)
      return next
    })
    for (const p of paths) ensureLoaded(p)
    ensureLoaded('/')
  }, [value, ensureLoaded])

  const toggle = useCallback((path: string) => {
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(path)) next.delete(path)
      else { next.add(path); ensureLoaded(path) }
      return next
    })
  }, [ensureLoaded])

  const submitCreate = useCallback(async (parent: string) => {
    const name = newName.trim()
    if (!name) { setCreateError(t('files.createFolderErrorEmpty')); return }
    if (name.includes('/') || name.includes('\\') || name === '..' || name === '.') {
      setCreateError(t('files.createFolderErrorChars'))
      return
    }
    const childPath = parent === '/' ? '/' + name : parent + '/' + name
    setCreateBusy(true)
    setCreateError(null)
    try {
      await apiClient.files.createDirectory(rootID, childPath)
      // Force refresh of parent listing + expand to reveal the new child.
      await fetchInto(parent)
      setExpanded((prev) => new Set(prev).add(parent))
      onChange(childPath)
      onFolderCreated?.(childPath)
      setCreatingUnder(null)
      setNewName('')
    } catch (err: any) {
      setCreateError(err?.body?.error ?? t('files.createFolderError'))
    } finally {
      setCreateBusy(false)
    }
  }, [newName, rootID, fetchInto, onChange, onFolderCreated, t])

  return (
    <div role="tree" className="max-h-80 overflow-auto rounded-sm border border-border bg-background py-1 text-sm">
      <Node
        rootName={rootName}
        path="/"
        depth={0}
        expanded={expanded}
        files={files}
        loading={loading}
        value={value}
        onSelect={onChange}
        onToggle={toggle}
        disabledPaths={disabledPaths}
        creatingUnder={creatingUnder}
        newName={newName}
        createBusy={createBusy}
        createError={createError}
        onStartCreate={(p) => {
          setCreatingUnder(p)
          setNewName('')
          setCreateError(null)
          setExpanded((prev) => new Set(prev).add(p))
          ensureLoaded(p)
        }}
        onCancelCreate={() => { setCreatingUnder(null); setNewName(''); setCreateError(null) }}
        onNewNameChange={setNewName}
        onSubmitCreate={submitCreate}
      />
    </div>
  )
}

type NodeProps = {
  rootName?: string
  path: string
  label?: string
  depth: number
  expanded: Set<string>
  files: Map<string, FileEntry[]>
  loading: Set<string>
  value: string
  onSelect: (path: string) => void
  onToggle: (path: string) => void
  disabledPaths?: Set<string>
  creatingUnder: string | null
  newName: string
  createBusy: boolean
  createError: string | null
  onStartCreate: (parent: string) => void
  onCancelCreate: () => void
  onNewNameChange: (s: string) => void
  onSubmitCreate: (parent: string) => void
}

function Node(props: NodeProps) {
  const {
    rootName, path, label, depth,
    expanded, files, loading, value,
    onSelect, onToggle, disabledPaths,
    creatingUnder, newName, createBusy, createError,
    onStartCreate, onCancelCreate, onNewNameChange, onSubmitCreate,
  } = props
  const { t } = useTranslation()
  const isExpanded = expanded.has(path)
  const isLoading  = loading.has(path)
  const childFiles = files.get(path)
  const childDirs  = childFiles ? directSubdirs(childFiles, path) : []
  const isSelected = value === path
  const disabled   = disabledPaths?.has(path) ?? false
  const displayLabel = label ?? rootName ?? path

  return (
    <div role="treeitem" aria-expanded={isExpanded} aria-selected={isSelected}>
      <div
        className={cn(
          'group flex items-center gap-1 mx-1 px-1 py-1 rounded-sm',
          !disabled && 'cursor-pointer hover:bg-accent/40',
          disabled && 'opacity-50 cursor-not-allowed',
          isSelected && 'bg-primary/15 text-primary font-medium ring-1 ring-primary/30',
        )}
        style={{ paddingLeft: 4 + depth * 12 }}
      >
        <button
          type="button"
          aria-label={t('files.treeExpand', { name: displayLabel })}
          onClick={(e) => { e.stopPropagation(); onToggle(path) }}
          className="flex h-4 w-4 items-center justify-center text-muted-foreground hover:text-foreground"
        >
          {isLoading
            ? <Loader2 className="h-3 w-3 animate-spin" />
            : <ChevronRight className={cn('h-3 w-3 transition-transform', isExpanded && 'rotate-90')} />}
        </button>
        <button
          type="button"
          disabled={disabled}
          onClick={() => { if (!disabled) onSelect(path) }}
          className="flex items-center gap-1.5 min-w-0 flex-1 text-left"
        >
          <Folder className="h-3.5 w-3.5 text-blue-500 shrink-0" />
          <span className="truncate" title={displayLabel}>{displayLabel}</span>
        </button>
        <button
          type="button"
          aria-label={t('files.bulkMoveNewFolder')}
          title={t('files.bulkMoveNewFolder')}
          onClick={(e) => { e.stopPropagation(); onStartCreate(path) }}
          className="can-hover:opacity-0 group-hover:opacity-100 hover:opacity-100 focus-visible:opacity-100 flex h-5 w-5 items-center justify-center rounded-sm text-muted-foreground hover:bg-accent hover:text-foreground"
        >
          <FolderPlus className="h-3.5 w-3.5" />
        </button>
      </div>
      {isExpanded && creatingUnder === path && (
        <div className="flex flex-col gap-1" style={{ paddingLeft: 4 + (depth + 1) * 12 + 18 }}>
          <div className="flex items-center gap-1">
            <Input
              autoFocus
              value={newName}
              onChange={(e) => onNewNameChange(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') { e.preventDefault(); onSubmitCreate(path) }
                else if (e.key === 'Escape') { e.preventDefault(); onCancelCreate() }
              }}
              placeholder={t('files.createFolderPlaceholder')}
              disabled={createBusy}
              className="h-7 text-sm flex-1"
            />
            <Button size="sm" onClick={() => onSubmitCreate(path)} disabled={createBusy}>
              {createBusy ? <Loader2 className="h-3 w-3 animate-spin" /> : t('files.createFolderSubmit')}
            </Button>
            <Button size="sm" variant="outline" onClick={onCancelCreate} disabled={createBusy}>
              {t('common.cancel')}
            </Button>
          </div>
          {createError && <p className="text-xs text-destructive">{createError}</p>}
        </div>
      )}
      {isExpanded && childDirs.length > 0 && (
        <div>
          {childDirs.map((name) => {
            const childPath = path === '/' ? '/' + name : path + '/' + name
            return (
              <Node
                {...props}
                key={childPath}
                path={childPath}
                label={name}
                depth={depth + 1}
              />
            )
          })}
        </div>
      )}
    </div>
  )
}
