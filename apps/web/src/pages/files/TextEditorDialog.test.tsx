import { describe, it, expect, vi, beforeEach } from 'vitest'
import { fireEvent, waitFor } from '@testing-library/react'
import { renderWithI18n } from '../../test/renderWithI18n'
import { TextEditorDialog } from './TextEditorDialog'
import { loadText, saveText } from './textFileIO'
import type { FileSource } from './fileSource'

// Monaco is untestable in jsdom (canvas + workers); swap it for a plain
// textarea / div so the dialog's behaviour stays observable.
vi.mock('@monaco-editor/react', () => ({
  __esModule: true,
  default: ({ value, onChange, options }: { value: string; onChange: (v: string) => void; options?: { readOnly?: boolean } }) => (
    <textarea
      data-testid="editor"
      value={value}
      readOnly={!!options?.readOnly}
      onChange={(e) => onChange(e.target.value)}
    />
  ),
  DiffEditor: ({ original, modified }: { original: string; modified: string }) => (
    <div data-testid="diff">{`SERVER:${original}|LOCAL:${modified}`}</div>
  ),
}))
vi.mock('./monacoSetup', () => ({ setupMonaco: vi.fn() }))
vi.mock('./textFileIO', () => ({ loadText: vi.fn(), saveText: vi.fn() }))

const mLoad = loadText as unknown as ReturnType<typeof vi.fn>
const mSave = saveText as unknown as ReturnType<typeof vi.fn>
const SRC: FileSource = { kind: 'root', rootID: 'r1' }

function renderDialog(canWrite = true, onShare?: () => void) {
  const onClose = vi.fn()
  const view = renderWithI18n(
    <TextEditorDialog source={SRC} path="/a.txt" name="a.txt" canWrite={canWrite} onShare={onShare} onClose={onClose} />,
  )
  return { ...view, onClose }
}

beforeEach(() => {
  vi.clearAllMocks()
  mLoad.mockResolvedValue({ content: 'hello', version: 3, hash: 'h' })
})

describe('TextEditorDialog', () => {
  it('laedt Inhalt; Speichern ist anfangs deaktiviert', async () => {
    const { findByTestId, getByRole } = renderDialog()
    const editor = await findByTestId('editor') as HTMLTextAreaElement
    expect(editor.value).toBe('hello')
    expect((getByRole('button', { name: 'Speichern' }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('aktiviert Speichern bei Aenderung und ruft saveText mit If-Match', async () => {
    mSave.mockResolvedValue({ status: 'ok', version: 4, contentHash: 'h2' })
    const { findByTestId, getByRole } = renderDialog()
    const editor = await findByTestId('editor') as HTMLTextAreaElement
    fireEvent.change(editor, { target: { value: 'edited' } })
    const save = getByRole('button', { name: 'Speichern' }) as HTMLButtonElement
    expect(save.disabled).toBe(false)
    fireEvent.click(save)
    await waitFor(() => expect(mSave).toHaveBeenCalledWith(SRC, '/a.txt', 'edited', { ifMatch: 3 }))
  })

  it('fragt beim Schliessen mit ungespeicherten Aenderungen nach', async () => {
    const { findByTestId, getByRole, getByText, onClose } = renderDialog()
    const editor = await findByTestId('editor') as HTMLTextAreaElement
    fireEvent.change(editor, { target: { value: 'dirty' } })
    fireEvent.click(getByRole('button', { name: 'Schließen' }))
    expect(getByText('Diese Datei hat ungespeicherte Änderungen. Beim Schließen gehen sie verloren.')).toBeTruthy()
    expect(onClose).not.toHaveBeenCalled()
    fireEvent.click(getByRole('button', { name: 'Verwerfen' }))
    expect(onClose).toHaveBeenCalled()
  })

  it('schliesst ohne Rueckfrage wenn nichts geaendert wurde', async () => {
    const { findByTestId, getByRole, onClose } = renderDialog()
    await findByTestId('editor')
    fireEvent.click(getByRole('button', { name: 'Schließen' }))
    expect(onClose).toHaveBeenCalled()
  })

  it('zeigt den Conflict-Diff und loest mit "Meine Version behalten"', async () => {
    mSave.mockResolvedValueOnce({ status: 'conflict', serverVersion: 9, serverHash: 'sh' })
    mLoad.mockResolvedValueOnce({ content: 'hello', version: 3, hash: 'h' })          // initial
    mLoad.mockResolvedValueOnce({ content: 'server-edit', version: 9, hash: 'sh' })   // re-fetch
    mSave.mockResolvedValueOnce({ status: 'ok', version: 10, contentHash: 'h4' })
    const { findByTestId, getByRole, getByText } = renderDialog()
    const editor = await findByTestId('editor') as HTMLTextAreaElement
    fireEvent.change(editor, { target: { value: 'mine' } })
    fireEvent.click(getByRole('button', { name: 'Speichern' }))
    await waitFor(() => expect(getByText('Konflikt: Datei wurde zwischenzeitlich geändert')).toBeTruthy())
    expect((await findByTestId('diff')).textContent).toBe('SERVER:server-edit|LOCAL:mine')
    fireEvent.click(getByRole('button', { name: 'Meine Version behalten' }))
    await waitFor(() => expect(mSave).toHaveBeenLastCalledWith(SRC, '/a.txt', 'mine', { ifMatch: 9 }))
  })

  it('versteckt Speichern bei fehlender Schreibberechtigung, Download bleibt', async () => {
    const { findByTestId, queryByRole } = renderDialog(false)
    const editor = await findByTestId('editor') as HTMLTextAreaElement
    expect(editor.readOnly).toBe(true)
    expect(queryByRole('button', { name: 'Speichern' })).toBeNull()
    expect(queryByRole('button', { name: /Herunterladen/ })).toBeTruthy()
  })

  it('zeigt Teilen nur wenn onShare gesetzt und ruft es auf', async () => {
    const onShare = vi.fn()
    const { findByTestId, getByRole } = renderDialog(true, onShare)
    await findByTestId('editor')
    fireEvent.click(getByRole('button', { name: /Teilen/ }))
    expect(onShare).toHaveBeenCalled()
  })

  it('versteckt Teilen ohne onShare', async () => {
    const { findByTestId, queryByRole } = renderDialog(true)
    await findByTestId('editor')
    expect(queryByRole('button', { name: /Teilen/ })).toBeNull()
  })

  it('spiegelt den Dirty-Status in der Statusleiste', async () => {
    const { findByTestId, getByText, queryByText } = renderDialog()
    const editor = await findByTestId('editor') as HTMLTextAreaElement
    expect(getByText('Gespeichert')).toBeTruthy()
    fireEvent.change(editor, { target: { value: 'changed' } })
    expect(getByText('Geändert')).toBeTruthy()
    expect(queryByText('Gespeichert')).toBeNull()
  })
})
