import { useTranslation } from '@privasync/i18n'
import { Input } from '@privasync/ui'
import { Search, X } from 'lucide-react'
import type { SyncRoot } from '@privasync/api-client'

interface FilesSearchBarProps {
  selectedRoot: SyncRoot | null
  value:    string
  setValue: (v: string) => void
  onCommit: () => void
  onClear:  () => void
}

/**
 * Toolbar search box used in the FilesPage top row. The placeholder
 * flips between per-root and global wording depending on whether a
 * sync root is open.
 */
export function FilesSearchBar(props: FilesSearchBarProps) {
  const { selectedRoot, value, setValue, onCommit, onClear } = props
  const { t } = useTranslation()
  const placeholder = selectedRoot
    ? t('files.searchPlaceholder')
    : t('files.searchPlaceholderGlobal')

  return (
    <div className="relative shrink-0 w-36 sm:w-64">
      <Search className="absolute left-2 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground pointer-events-none" />
      <Input
        type="text"
        value={value}
        onChange={(e) => setValue(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter') {
            e.preventDefault()
            onCommit()
          } else if (e.key === 'Escape') {
            e.preventDefault()
            onClear()
          }
        }}
        placeholder={placeholder}
        aria-label={placeholder}
        className="h-8 pl-7 pr-7 text-sm"
      />
      {value && (
        <button
          type="button"
          onClick={onClear}
          aria-label={t('files.searchClear')}
          title={t('files.searchClear')}
          className="absolute right-1 top-1/2 -translate-y-1/2 rounded-sm p-1 text-muted-foreground hover:text-foreground hover:bg-accent"
        >
          <X className="h-3.5 w-3.5" />
        </button>
      )}
    </div>
  )
}
