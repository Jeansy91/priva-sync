import { MoreVertical } from 'lucide-react'
import { DropdownMenu } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import { type DirEntry } from './helpers'
import { buildEntryMenuItems, type EntryMenuCallbacks } from './entryMenuItems'

// The `...`-trigger sibling of EntryContextMenu. Both share the same items
// via buildEntryMenuItems so the right-click menu and the row's overflow
// menu can never drift out of sync.
export function EntryMenu({
  entry,
  callbacks,
}: {
  entry:     DirEntry
  callbacks: EntryMenuCallbacks
}) {
  const { t } = useTranslation()
  const items = buildEntryMenuItems([entry], callbacks, t)
  return (
    <DropdownMenu
      triggerLabel={t('files.actionsMenu')}
      trigger={<MoreVertical className="h-4 w-4" />}
      items={items}
      className="can-hover:opacity-0 group-hover:opacity-100 focus-within:opacity-100"
    />
  )
}
