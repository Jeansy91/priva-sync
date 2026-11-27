import {
  forwardRef, useCallback, useImperativeHandle, useRef, useState,
} from 'react'
import { Upload, ChevronDown, Folder } from 'lucide-react'
import { Button } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import { useClickOutside } from './helpers'

export interface UploadItem {
  file:    File
  relPath: string
}

export interface UploadSplitButtonHandle {
  openFilePicker: () => void
}

// FileList → upload items. webkitRelativePath is set when the picker was
// opened with webkitdirectory — it then carries the full path including the
// selected folder as the first segment (e.g. "docs/sub/foo.txt").
export function toUploadItems(list: FileList | File[]): UploadItem[] {
  return Array.from(list).map((f) => {
    const rel = (f as File & { webkitRelativePath?: string }).webkitRelativePath
    return { file: f, relPath: rel && rel.length > 0 ? rel : f.name }
  })
}

export const UploadSplitButton = forwardRef<UploadSplitButtonHandle, {
  onFiles: (items: UploadItem[]) => void
}>(function UploadSplitButton({ onFiles }, ref) {
  const { t } = useTranslation()
  const fileInputRef = useRef<HTMLInputElement>(null)
  const dirInputRef  = useRef<HTMLInputElement>(null)
  const [menuOpen, setMenuOpen] = useState(false)
  const menuRef = useRef<HTMLDivElement>(null)
  useClickOutside(menuRef, menuOpen, () => setMenuOpen(false))

  useImperativeHandle(ref, () => ({
    openFilePicker: () => fileInputRef.current?.click(),
  }), [])

  // webkitdirectory ist kein React-bekanntes Prop → Callback-Ref setzt das
  // Attribut, sobald der Input ins DOM kommt.
  const attachDirInput = useCallback((el: HTMLInputElement | null) => {
    dirInputRef.current = el
    if (el) {
      el.setAttribute('webkitdirectory', '')
      el.setAttribute('directory', '')
    }
  }, [])

  const onChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    if (e.target.files?.length) {
      onFiles(toUploadItems(e.target.files))
      e.target.value = ''
    }
  }

  return (
    <>
      <div ref={menuRef} className="relative flex items-center">
        <Button
          size="sm"
          className="rounded-r-none"
          onClick={() => fileInputRef.current?.click()}
          aria-label={t('files.upload')}
          title={t('files.upload')}
        >
          <Upload className="h-3.5 w-3.5" />
          <span className="hidden sm:inline">{t('files.upload')}</span>
        </Button>
        <Button
          size="sm"
          className="rounded-l-none border-l border-primary-foreground/20 px-1.5"
          onClick={() => setMenuOpen((p) => !p)}
          title={t('files.uploadOptions')}
          aria-label={t('files.uploadOptions')}
        >
          <ChevronDown className="h-3.5 w-3.5" />
        </Button>
        {menuOpen && (
          <div className="absolute right-0 top-9 z-20 min-w-48 rounded-md border border-border bg-popover shadow-md py-1">
            <button
              className="flex w-full items-center gap-2 px-3 py-1.5 text-sm hover:bg-accent"
              onClick={() => { setMenuOpen(false); fileInputRef.current?.click() }}
            >
              <Upload className="h-3.5 w-3.5" /> {t('files.uploadFiles')}
            </button>
            <button
              className="flex w-full items-center gap-2 px-3 py-1.5 text-sm hover:bg-accent"
              onClick={() => { setMenuOpen(false); dirInputRef.current?.click() }}
            >
              <Folder className="h-3.5 w-3.5" /> {t('files.uploadFolder')}
            </button>
          </div>
        )}
      </div>
      <input ref={fileInputRef}   type="file" multiple className="hidden" onChange={onChange} />
      <input ref={attachDirInput} type="file" multiple className="hidden" onChange={onChange} />
    </>
  )
})
