import { useState } from 'react'
import { Button, Checkbox, FileIcon } from '@privasync/ui'
import { RotateCcw, Trash2, Loader2, ChevronRight, ChevronDown, Folder } from 'lucide-react'
import type { SyncRoot } from '@privasync/api-client'
import { TitleWrap } from '../../components/TitleWrap'
import {
  buildTrashTree, collectFileIds, collectFileEntries, aggregateSize, latestDeletedAt,
  fileName, formatBytes, formatDate,
  type TreeNode, type TrashEntryWithRoot, type TrashLabels,
} from './trashHelpers'

export interface TrashTreeProps {
  roots:        SyncRoot[]
  entries:      TrashEntryWithRoot[]
  selectedIds:  Set<string>
  setSelectedIds: React.Dispatch<React.SetStateAction<Set<string>>>
  localeTag:    string
  labels:       TrashLabels
  onRestore:    (entry: TrashEntryWithRoot) => void
  onPurge:      (entry: TrashEntryWithRoot) => void
  onRestoreMany: (entries: TrashEntryWithRoot[]) => void
  onPurgeMany:  (entries: TrashEntryWithRoot[]) => void
  restoring:    string | null
  purging:      string | null
  busy:         boolean
}

/**
 * Tree view of trashed files grouped by sync root and folder. Renders a
 * flattened, virtual-friendly list of visible nodes by walking the tree
 * with the current expansion set. Selection cascades across the file
 * descendants of a folder when its checkbox is toggled.
 */
export function TrashTree(props: TrashTreeProps) {
  const {
    roots, entries, selectedIds, setSelectedIds, localeTag, labels,
    onRestore, onPurge, onRestoreMany, onPurgeMany, restoring, purging, busy,
  } = props
  const tree = buildTrashTree(roots, entries)
  // Everything collapsed on mount — only sync-root top nodes are visible;
  // folders expand on click.
  const [expandedIds, setExpandedIds] = useState<Set<string>>(() => new Set())

  type Visible = { node: TreeNode; level: number }
  const visible: Visible[] = []
  const walk = (n: TreeNode, level: number) => {
    visible.push({ node: n, level })
    if (n.kind === 'file') return
    if (!expandedIds.has(n.id)) return
    n.children.forEach((c) => walk(c, level + 1))
  }
  tree.forEach((n) => walk(n, 1))

  const toggleExpand = (id: string) => {
    setExpandedIds((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id); else next.add(id)
      return next
    })
  }

  return (
    <div className="relative rounded-lg border border-border overflow-x-auto">
      <table className="w-full text-sm">
        <thead className="sticky top-0 z-10 bg-muted/95 backdrop-blur-sm">
          <tr className="border-b border-border text-muted-foreground">
            <th className="px-3 py-2.5 w-8">
              <HeaderCheckbox
                total={entries.length}
                selectedCount={selectedIds.size}
                label={labels.selectAll}
                onToggle={(check) => {
                  setSelectedIds(check ? new Set(entries.map((e) => e.id)) : new Set())
                }}
              />
            </th>
            <th className="text-left px-4 py-2.5 font-medium w-full">{labels.colFile}</th>
            <th className="text-right px-4 py-2.5 font-medium whitespace-nowrap hidden sm:table-cell">{labels.colSize}</th>
            <th className="text-right px-4 py-2.5 font-medium whitespace-nowrap hidden md:table-cell">{labels.colDeletedAt}</th>
            <th className="px-3 py-2.5 w-8" />
          </tr>
        </thead>
        <tbody role="tree">
          {visible.map(({ node, level }) => (
            <TrashTreeRow
              key={node.id}
              node={node}
              level={level}
              expanded={node.kind === 'file' ? undefined : expandedIds.has(node.id)}
              onToggleExpand={() => toggleExpand(node.id)}
              selectedIds={selectedIds}
              setSelectedIds={setSelectedIds}
              localeTag={localeTag}
              labels={labels}
              onRestore={onRestore}
              onPurge={onPurge}
              onRestoreMany={onRestoreMany}
              onPurgeMany={onPurgeMany}
              restoring={restoring}
              purging={purging}
              busy={busy}
            />
          ))}
        </tbody>
      </table>
    </div>
  )
}

interface TrashTreeRowProps {
  node:          TreeNode
  level:         number
  expanded:      boolean | undefined
  onToggleExpand: () => void
  selectedIds:   Set<string>
  setSelectedIds: React.Dispatch<React.SetStateAction<Set<string>>>
  localeTag:     string
  labels:        TrashLabels
  onRestore:     TrashTreeProps['onRestore']
  onPurge:       TrashTreeProps['onPurge']
  onRestoreMany: TrashTreeProps['onRestoreMany']
  onPurgeMany:   TrashTreeProps['onPurgeMany']
  restoring:     string | null
  purging:       string | null
  busy:          boolean
}

function TrashTreeRow(props: TrashTreeRowProps) {
  const {
    node, level, expanded, onToggleExpand,
    selectedIds, setSelectedIds, localeTag, labels,
    onRestore, onPurge, onRestoreMany, onPurgeMany, restoring, purging, busy,
  } = props
  const isLeaf = node.kind === 'file'
  const allIds = collectFileIds(node)
  const selectedCount = allIds.filter((id) => selectedIds.has(id)).length
  const allChecked = allIds.length > 0 && selectedCount === allIds.length
  const indeterminate = selectedCount > 0 && selectedCount < allIds.length

  const label = isLeaf ? fileName(node.entry.path) : node.name
  const sizeBytes = isLeaf ? node.entry.size : aggregateSize(node)
  const deletedAt = isLeaf ? node.entry.deleted_at : latestDeletedAt(node)
  const indent = (level - 1) * 16

  return (
    <tr
      role="treeitem"
      aria-label={label}
      aria-level={level}
      aria-expanded={isLeaf ? undefined : expanded}
      aria-selected={isLeaf ? selectedIds.has(node.id) : undefined}
      className="border-b border-border/50 last:border-0 hover:bg-accent/20 transition-colors"
    >
      <td className="px-3 py-2.5 w-8">
        <Checkbox
          aria-label={label}
          checked={isLeaf ? selectedIds.has(node.id) : allChecked}
          indeterminate={!isLeaf && indeterminate}
          onChange={(e) => {
            const check = e.target.checked
            setSelectedIds((prev) => {
              const next = new Set(prev)
              for (const id of allIds) {
                if (check) next.add(id); else next.delete(id)
              }
              return next
            })
          }}
        />
      </td>
      <td className="px-4 py-2.5 max-w-0">
        <div className="flex items-center gap-1.5 min-w-0" style={{ paddingLeft: indent }}>
          {!isLeaf ? (
            <button
              type="button"
              aria-label={expanded ? labels.collapse : labels.expand}
              className="h-5 w-5 flex items-center justify-center text-muted-foreground shrink-0"
              onClick={onToggleExpand}
            >
              {expanded
                ? <ChevronDown className="h-3.5 w-3.5" />
                : <ChevronRight className="h-3.5 w-3.5" />}
            </button>
          ) : <span className="w-5 shrink-0" />}
          {isLeaf
            ? <FileIcon name={fileName(node.entry.path)} className="h-4 w-4" />
            : <Folder className="h-4 w-4 text-blue-500 shrink-0" />}
          <div className="min-w-0">
            <p className="truncate font-medium">{label}</p>
          </div>
        </div>
      </td>
      <td className="px-4 py-2.5 text-right text-muted-foreground whitespace-nowrap hidden sm:table-cell">
        {formatBytes(sizeBytes)}
      </td>
      <td className="px-4 py-2.5 text-right text-muted-foreground whitespace-nowrap hidden md:table-cell">
        {formatDate(deletedAt, localeTag)}
      </td>
      <td className="px-3 py-2 text-right">
        <div className="flex items-center justify-end gap-1">
          <TitleWrap title={labels.restore}>
            <Button
              variant="ghost"
              size="icon"
              className="h-7 w-7"
              onClick={() => {
                if (isLeaf) onRestore(node.entry)
                else onRestoreMany(collectFileEntries(node))
              }}
              disabled={(isLeaf && (restoring === node.id || purging === node.id)) || busy}
              aria-label={labels.restore}
            >
              {isLeaf && restoring === node.id
                ? <Loader2 className="h-3.5 w-3.5 animate-spin" />
                : <RotateCcw className="h-3.5 w-3.5" />}
            </Button>
          </TitleWrap>
          <TitleWrap title={labels.purgeOne}>
            <Button
              variant="ghost"
              size="icon"
              className="h-7 w-7 text-destructive hover:text-destructive hover:bg-destructive/10"
              onClick={() => {
                if (isLeaf) onPurge(node.entry)
                else onPurgeMany(collectFileEntries(node))
              }}
              disabled={(isLeaf && (restoring === node.id || purging === node.id)) || busy}
              aria-label={labels.purgeOne}
            >
              {isLeaf && purging === node.id
                ? <Loader2 className="h-3.5 w-3.5 animate-spin" />
                : <Trash2 className="h-3.5 w-3.5" />}
            </Button>
          </TitleWrap>
        </div>
      </td>
    </tr>
  )
}

interface HeaderCheckboxProps {
  total:         number
  selectedCount: number
  label:         string
  onToggle:      (check: boolean) => void
}

export function HeaderCheckbox({ total, selectedCount, label, onToggle }: HeaderCheckboxProps) {
  const allChecked = total > 0 && selectedCount === total
  const indeterminate = selectedCount > 0 && selectedCount < total
  // Master checkbox intent is derived from the current selection, not from the
  // native toggled value: clicking an indeterminate box makes the browser set
  // `checked = true`, so reading `e.target.checked` is unreliable. Only a full
  // selection clears; empty or partial first selects everything.
  return (
    <Checkbox
      aria-label={label}
      checked={allChecked}
      indeterminate={indeterminate}
      onChange={() => onToggle(!allChecked)}
    />
  )
}
