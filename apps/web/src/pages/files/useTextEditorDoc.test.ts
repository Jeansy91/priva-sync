import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useTextEditorDoc } from './useTextEditorDoc'
import { loadText, saveText } from './textFileIO'
import type { FileSource } from './fileSource'

vi.mock('./textFileIO', () => ({
  loadText: vi.fn(),
  saveText: vi.fn(),
}))

const mLoad = loadText as unknown as ReturnType<typeof vi.fn>
const mSave = saveText as unknown as ReturnType<typeof vi.fn>
const SRC: FileSource = { kind: 'root', rootID: 'r1' }

beforeEach(() => {
  vi.clearAllMocks()
  mLoad.mockResolvedValue({ content: 'hello', version: 3, hash: 'h' })
})

async function mountReady() {
  const view = renderHook(() => useTextEditorDoc(SRC, '/a.txt', 'a.txt'))
  await waitFor(() => expect(view.result.current.status).toBe('ready'))
  return view
}

describe('useTextEditorDoc — load', () => {
  it('laedt Inhalt und setzt Sprache nach Dateiname', async () => {
    const { result } = renderHook(() => useTextEditorDoc(SRC, '/a.ts', 'a.ts'))
    await waitFor(() => expect(result.current.status).toBe('ready'))
    expect(result.current.content).toBe('hello')
    expect(result.current.isDirty).toBe(false)
    expect(result.current.language).toBe('typescript')
  })

  it('setzt error-Status bei fehlgeschlagenem Laden', async () => {
    mLoad.mockRejectedValueOnce(new Error('boom'))
    const { result } = renderHook(() => useTextEditorDoc(SRC, '/a.txt', 'a.txt'))
    await waitFor(() => expect(result.current.status).toBe('error'))
    expect(result.current.errorKey).toBe('files.editorLoadFailed')
  })
})

describe('useTextEditorDoc — dirty + save', () => {
  it('wird dirty bei Inhaltsaenderung', async () => {
    const { result } = await mountReady()
    act(() => result.current.setContent('hello!'))
    expect(result.current.isDirty).toBe(true)
  })

  it('speichert mit If-Match der geladenen Version und wird wieder clean', async () => {
    mSave.mockResolvedValue({ status: 'ok', version: 4, contentHash: 'h2' })
    const { result } = await mountReady()
    act(() => result.current.setContent('edited'))
    await act(async () => { await result.current.save() })
    expect(mSave).toHaveBeenCalledWith(SRC, '/a.txt', 'edited', { ifMatch: 3 })
    expect(result.current.isDirty).toBe(false)
    expect(result.current.status).toBe('ready')
  })

  it('nutzt die neue Version fuer den zweiten Save', async () => {
    mSave.mockResolvedValueOnce({ status: 'ok', version: 4, contentHash: 'h2' })
    mSave.mockResolvedValueOnce({ status: 'ok', version: 5, contentHash: 'h3' })
    const { result } = await mountReady()
    act(() => result.current.setContent('one'))
    await act(async () => { await result.current.save() })
    act(() => result.current.setContent('two'))
    await act(async () => { await result.current.save() })
    expect(mSave).toHaveBeenLastCalledWith(SRC, '/a.txt', 'two', { ifMatch: 4 })
  })

  it('setzt error-Status bei Save-Fehler', async () => {
    mSave.mockRejectedValue(new Error('net'))
    const { result } = await mountReady()
    act(() => result.current.setContent('x'))
    await act(async () => { await result.current.save() })
    expect(result.current.status).toBe('error')
    expect(result.current.errorKey).toBe('files.editorSaveFailed')
  })
})

describe('useTextEditorDoc — conflict', () => {
  it('oeffnet Conflict-State mit lokalem und Server-Inhalt', async () => {
    mSave.mockResolvedValue({ status: 'conflict', serverVersion: 9, serverHash: 'sh' })
    mLoad.mockResolvedValueOnce({ content: 'hello', version: 3, hash: 'h' }) // initial load
    mLoad.mockResolvedValueOnce({ content: 'server-changed', version: 9, hash: 'sh' }) // re-fetch
    const { result } = await mountReady()
    act(() => result.current.setContent('mine'))
    await act(async () => { await result.current.save() })
    expect(result.current.conflict).toEqual({
      localContent:  'mine',
      serverContent: 'server-changed',
      serverVersion: 9,
    })
  })

  it('keep-mine speichert mit der Server-Version als If-Match', async () => {
    mSave.mockResolvedValueOnce({ status: 'conflict', serverVersion: 9, serverHash: 'sh' })
    mLoad.mockResolvedValueOnce({ content: 'hello', version: 3, hash: 'h' })
    mLoad.mockResolvedValueOnce({ content: 'server-changed', version: 9, hash: 'sh' })
    mSave.mockResolvedValueOnce({ status: 'ok', version: 10, contentHash: 'h4' })
    const { result } = await mountReady()
    act(() => result.current.setContent('mine'))
    await act(async () => { await result.current.save() })
    await act(async () => { await result.current.resolveKeepMine() })
    expect(mSave).toHaveBeenLastCalledWith(SRC, '/a.txt', 'mine', { ifMatch: 9 })
    expect(result.current.conflict).toBeNull()
    expect(result.current.isDirty).toBe(false)
  })

  it('keep-server uebernimmt Server-Inhalt und wird clean', async () => {
    mSave.mockResolvedValue({ status: 'conflict', serverVersion: 9, serverHash: 'sh' })
    mLoad.mockResolvedValueOnce({ content: 'hello', version: 3, hash: 'h' })
    mLoad.mockResolvedValueOnce({ content: 'server-changed', version: 9, hash: 'sh' })
    const { result } = await mountReady()
    act(() => result.current.setContent('mine'))
    await act(async () => { await result.current.save() })
    act(() => result.current.resolveKeepServer())
    expect(result.current.content).toBe('server-changed')
    expect(result.current.isDirty).toBe(false)
    expect(result.current.conflict).toBeNull()
  })
})
