import { useCallback, useEffect, useRef, useState } from 'react'
import Editor, { type OnMount } from '@monaco-editor/react'
import { useTranslation } from '@privasync/i18n'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, DialogDescription, Button } from '@privasync/ui'
import { AlertCircle, Download, FileCode2, FileText, Loader2, Minus, Plus, Save, Share2, X } from 'lucide-react'
import { setupMonaco } from './monacoSetup'
import { monacoTheme } from './monacoTheme'
import { ConflictDiffDialog } from './ConflictDiffDialog'
import { useTextEditorDoc } from './useTextEditorDoc'
import type { FileSource } from './fileSource'

const FONT_KEY = 'privasync.editor.fontSize'
const FONT_MIN = 10
const FONT_MAX = 24

function initialFontSize(): number {
  const raw = typeof localStorage !== 'undefined' ? localStorage.getItem(FONT_KEY) : null
  const n = raw ? Number(raw) : NaN
  return Number.isFinite(n) ? Math.min(FONT_MAX, Math.max(FONT_MIN, n)) : 14
}

// Pretty display name for the status bar; falls back to the raw Monaco id.
const LANG_LABEL: Record<string, string> = {
  typescript: 'TypeScript', javascript: 'JavaScript', json: 'JSON', markdown: 'Markdown',
  yaml: 'YAML', shell: 'Shell', python: 'Python', go: 'Go', rust: 'Rust', html: 'HTML',
  css: 'CSS', scss: 'SCSS', less: 'LESS', xml: 'XML', sql: 'SQL', ini: 'INI', plaintext: 'Text',
}

function languageLabel(id: string): string {
  return LANG_LABEL[id] ?? id.charAt(0).toUpperCase() + id.slice(1)
}

function isCodeLang(id: string): boolean {
  return id !== 'plaintext' && id !== 'markdown'
}

// Download the current editor buffer (including unsaved edits) as a file.
function downloadBuffer(name: string, content: string): void {
  const url = URL.createObjectURL(new Blob([content], { type: 'text/plain;charset=utf-8' }))
  const a = document.createElement('a')
  a.href = url
  a.download = name
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}

// TextEditorDialog hosts the Monaco editor for one text file in a modern,
// lightweight shell: a toolbar (share / download / save), a language-aware
// title, and a status bar (language · encoding · cursor · dirty state). All
// load/save/conflict behaviour lives in useTextEditorDoc.
export function TextEditorDialog({
  source, path, name, canWrite, onShare, onClose,
}: {
  source:   FileSource
  path:     string
  name:     string
  canWrite: boolean
  onShare?: () => void
  onClose:  () => void
}) {
  const { t } = useTranslation()
  const doc = useTextEditorDoc(source, path, name)
  const [fontSize, setFontSize] = useState(initialFontSize)
  const [confirmClose, setConfirmClose] = useState(false)
  const [pos, setPos] = useState({ line: 1, col: 1 })

  setupMonaco()

  useEffect(() => {
    if (typeof localStorage !== 'undefined') localStorage.setItem(FONT_KEY, String(fontSize))
  }, [fontSize])

  // Latest-save ref so the Monaco Strg+S command (registered once on mount)
  // always calls the current save closure.
  const saveRef = useRef(doc.save)
  saveRef.current = doc.save

  const handleMount: OnMount = useCallback((editor, monaco) => {
    editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => {
      void saveRef.current()
    })
    editor.onDidChangeCursorPosition((e) => {
      setPos({ line: e.position.lineNumber, col: e.position.column })
    })
  }, [])

  // Closing with unsaved edits asks first; a clean doc closes immediately.
  const requestClose = useCallback(() => {
    if (doc.isDirty) setConfirmClose(true)
    else onClose()
  }, [doc.isDirty, onClose])

  const saving = doc.status === 'saving'
  const canSave = canWrite && doc.isDirty && !saving
  const FileGlyph = isCodeLang(doc.language) ? FileCode2 : FileText

  return (
    <>
      <Dialog open onOpenChange={(o) => { if (!o) requestClose() }}>
        <DialogContent className="max-w-5xl w-[95vw] h-[85vh] p-0 gap-0 overflow-hidden rounded-xl flex flex-col">
          {/* Toolbar */}
          <div className="flex items-center justify-between gap-3 h-12 px-3 border-b border-border bg-muted/40 shrink-0">
            <div className="flex items-center gap-2 min-w-0">
              <FileGlyph className="h-4 w-4 text-muted-foreground shrink-0" />
              <span className="truncate text-sm font-medium" title={name}>{name}</span>
              {doc.isDirty && (
                <span
                  className="h-1.5 w-1.5 rounded-full bg-amber-500 shrink-0"
                  aria-label={t('files.editorUnsavedMark')}
                  title={t('files.editorUnsavedMark')}
                />
              )}
              {!canWrite && (
                <span className="rounded-full border border-border px-2 py-0.5 text-[11px] text-muted-foreground shrink-0">
                  {t('files.editorReadOnly')}
                </span>
              )}
            </div>

            <div className="flex items-center gap-1 shrink-0">
              {onShare && (
                <Button variant="ghost" size="sm" className="gap-1.5" onClick={onShare}>
                  <Share2 className="h-4 w-4" />
                  <span className="hidden sm:inline">{t('files.menuShare')}</span>
                </Button>
              )}
              <Button variant="ghost" size="sm" className="gap-1.5" onClick={() => downloadBuffer(name, doc.content)} disabled={doc.status === 'loading'}>
                <Download className="h-4 w-4" />
                <span className="hidden sm:inline">{t('files.menuDownload')}</span>
              </Button>
              {canWrite && (
                <Button variant="default" size="sm" className="gap-1.5" onClick={() => void doc.save()} disabled={!canSave}>
                  {saving ? <Loader2 className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />}
                  <span>{t('files.editorSave')}</span>
                </Button>
              )}
              <div className="mx-1 h-5 w-px bg-border" />
              <Button variant="ghost" size="icon" onClick={requestClose} aria-label={t('common.close')}>
                <X className="h-4 w-4" />
              </Button>
            </div>
          </div>

          {doc.errorKey && (
            <div className="flex items-center gap-2 px-4 py-2 text-sm text-destructive bg-destructive/5 border-b border-border shrink-0">
              <AlertCircle className="h-4 w-4 shrink-0" />
              <span>{t(doc.errorKey)}</span>
            </div>
          )}

          {/* Editor */}
          <div className="flex-1 min-h-0 bg-background">
            {doc.status === 'loading' ? (
              <div className="flex items-center justify-center h-full">
                <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
              </div>
            ) : (
              <Editor
                value={doc.content}
                language={doc.language}
                theme={monacoTheme()}
                onMount={handleMount}
                onChange={(v) => doc.setContent(v ?? '')}
                options={{
                  readOnly:             !canWrite,
                  fontSize,
                  minimap:              { enabled: false },
                  scrollBeyondLastLine: false,
                  automaticLayout:      true,
                  padding:              { top: 12, bottom: 12 },
                  smoothScrolling:      true,
                  renderLineHighlight:  'all',
                  roundedSelection:     true,
                  scrollbar:            { verticalScrollbarSize: 10, horizontalScrollbarSize: 10 },
                }}
              />
            )}
          </div>

          {/* Status bar */}
          <div className="flex items-center justify-between h-7 px-3 text-[11px] text-muted-foreground border-t border-border bg-muted/40 shrink-0 select-none">
            <div className="flex items-center gap-3">
              <span className="font-medium text-foreground/70">{languageLabel(doc.language)}</span>
              <span>UTF-8</span>
              <span className={doc.isDirty ? 'text-amber-500' : 'text-emerald-500'}>
                {doc.isDirty ? t('files.editorStatusModified') : t('files.editorStatusSaved')}
              </span>
            </div>
            <div className="flex items-center gap-2">
              <span className="tabular-nums">{t('files.editorLineColumn', { line: pos.line, col: pos.col })}</span>
              <div className="flex items-center gap-0.5">
                <button
                  type="button"
                  onClick={() => setFontSize((s) => Math.max(FONT_MIN, s - 1))}
                  aria-label={t('files.editorFontSmaller')}
                  className="rounded-sm p-0.5 hover:bg-accent hover:text-accent-foreground"
                >
                  <Minus className="h-3 w-3" />
                </button>
                <span className="tabular-nums w-5 text-center">{fontSize}</span>
                <button
                  type="button"
                  onClick={() => setFontSize((s) => Math.min(FONT_MAX, s + 1))}
                  aria-label={t('files.editorFontLarger')}
                  className="rounded-sm p-0.5 hover:bg-accent hover:text-accent-foreground"
                >
                  <Plus className="h-3 w-3" />
                </button>
              </div>
            </div>
          </div>
        </DialogContent>
      </Dialog>

      {doc.conflict && (
        <ConflictDiffDialog
          conflict={doc.conflict}
          language={doc.language}
          busy={saving}
          onKeepMine={() => void doc.resolveKeepMine()}
          onKeepServer={doc.resolveKeepServer}
          onDismiss={doc.dismissConflict}
        />
      )}

      <Dialog open={confirmClose} onOpenChange={(o) => { if (!o) setConfirmClose(false) }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('files.editorUnsavedTitle')}</DialogTitle>
            <DialogDescription>{t('files.editorUnsavedDesc')}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setConfirmClose(false)}>
              {t('files.editorKeepEditing')}
            </Button>
            <Button variant="destructive" onClick={() => { setConfirmClose(false); onClose() }}>
              {t('files.editorDiscard')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
