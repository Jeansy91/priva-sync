import { Dialog, DialogContent, DialogClose } from '@privasync/ui'
import { ArchiveViewer } from './ArchiveViewer'
import { type FileSource } from './fileSource'

export function ArchiveViewerDialog({
  source, path, name, onClose,
}: {
  source:  FileSource
  path:    string
  name:    string
  onClose: () => void
}) {
  return (
    <Dialog open onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent className="max-w-3xl w-[95vw] h-[80vh] p-0 flex flex-col">
        <div className="flex items-center justify-between px-4 py-2 border-b border-border shrink-0">
          <div className="truncate text-sm font-medium" title={name}>{name}</div>
          <DialogClose onClose={onClose} className="static" />
        </div>
        <div className="flex-1 min-h-0 overflow-auto">
          <ArchiveViewer source={source} path={path} name={name} />
        </div>
      </DialogContent>
    </Dialog>
  )
}
