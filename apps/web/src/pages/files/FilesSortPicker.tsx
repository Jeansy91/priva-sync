import {
  ArrowDown01, ArrowDown10, ArrowDownAZ, ArrowDownZA, ArrowDownUp,
  Check, ClockArrowDown, ClockArrowUp,
  type LucideIcon,
} from 'lucide-react'
import { DropdownMenu, type DropdownMenuItem } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import type { FilesSortKey, FilesViewSort } from './filesViewTypes'
import type { SortDir } from '../../hooks/useTableSort'

type Props = {
  sort: FilesViewSort
}

type Combo = { key: FilesSortKey; dir: SortDir }

const COMBOS: Combo[] = [
  { key: 'name',  dir: 'asc'  },
  { key: 'name',  dir: 'desc' },
  { key: 'size',  dir: 'asc'  },
  { key: 'size',  dir: 'desc' },
  { key: 'mtime', dir: 'asc'  },
  { key: 'mtime', dir: 'desc' },
]

const LABEL_KEY: Record<FilesSortKey, string> = {
  name:  'files.colName',
  size:  'files.colSize',
  mtime: 'files.colMtime',
}

// Icon-Matrix: Trigger zeigt den aktuell aktiven Combo, jeder Menüpunkt
// dasselbe Symbol vor seinem Label.
const ICON: Record<FilesSortKey, Record<SortDir, LucideIcon>> = {
  name:  { asc: ArrowDownAZ,   desc: ArrowDownZA   },
  size:  { asc: ArrowDown01,   desc: ArrowDown10   },
  mtime: { asc: ClockArrowUp,  desc: ClockArrowDown },
}

export function FilesSortPicker({ sort }: Props) {
  const { t } = useTranslation()

  const items: DropdownMenuItem[] = COMBOS.map(({ key, dir }) => {
    const active = key === sort.sortKey && dir === sort.sortDir
    const RowIcon = ICON[key][dir]
    return {
      label: `${t(LABEL_KEY[key])} ${dir === 'asc' ? '↑' : '↓'}`,
      icon: (
        <span className="inline-flex items-center gap-2">
          <Check className={active ? 'h-3.5 w-3.5 text-foreground' : 'h-3.5 w-3.5 opacity-0'} />
          <RowIcon className="h-3.5 w-3.5 text-muted-foreground" />
        </span>
      ),
      onSelect: () => sort.setSort(key, dir),
    }
  })

  // Default-State (kein Sort gesetzt) → generisches ArrowDownUp; sobald ein
  // Combo aktiv ist, spiegelt der Trigger ihn wider.
  const TriggerIcon = sort.sortKey && sort.sortDir
    ? ICON[sort.sortKey][sort.sortDir]
    : ArrowDownUp

  return (
    <DropdownMenu
      triggerLabel={t('files.sortBy')}
      trigger={<TriggerIcon className="h-4 w-4" />}
      items={items}
    />
  )
}
