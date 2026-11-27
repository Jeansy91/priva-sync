import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, fireEvent } from '@testing-library/react'
import { renderWithI18n } from '../../test/renderWithI18n'
import { downloadBackupCodes, BackupCodesDisplay } from './BackupCodes'

const t = (k: string) => k

describe('downloadBackupCodes', () => {
  beforeEach(() => {
    URL.createObjectURL = vi.fn(() => 'blob:mock-url')
    URL.revokeObjectURL = vi.fn()
  })

  it('creates and revokes an object URL, and triggers a click via an anchor', () => {
    const clickSpy = vi.fn()
    const createElementSpy = vi.spyOn(document, 'createElement')

    downloadBackupCodes(['aaaa-bbbb', 'cccc-dddd'], 'alice', t)

    expect(URL.createObjectURL).toHaveBeenCalledTimes(1)
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:mock-url')
    expect(createElementSpy).toHaveBeenCalledWith('a')
  })

  it('names the file after the username', () => {
    let captured: HTMLAnchorElement | undefined
    const appendChildSpy = vi.spyOn(document.body, 'appendChild').mockImplementation((node) => {
      captured = node as HTMLAnchorElement
      Object.defineProperty(captured, 'click', { value: vi.fn(), configurable: true })
      return node
    })
    vi.spyOn(document.body, 'removeChild').mockImplementation((node) => node)

    downloadBackupCodes(['code-1'], 'bob', t)

    expect(captured?.download).toBe('privasync-backup-codes-bob.txt')
    appendChildSpy.mockRestore()
  })
})

describe('BackupCodesDisplay', () => {
  it('renders all codes', () => {
    renderWithI18n(
      <BackupCodesDisplay codes={['a1a1-a1a1', 'b2b2-b2b2']} username="alice" onDone={vi.fn()} />,
    )
    expect(screen.getByText('a1a1-a1a1')).toBeInTheDocument()
    expect(screen.getByText('b2b2-b2b2')).toBeInTheDocument()
  })

  it('calls onDone when the done button is clicked', () => {
    const onDone = vi.fn()
    renderWithI18n(<BackupCodesDisplay codes={['a1a1-a1a1']} username="alice" onDone={onDone} />)

    fireEvent.click(screen.getByText('Ich habe die Codes gesichert'))
    expect(onDone).toHaveBeenCalled()
  })

  it('triggers a download when the download button is clicked', () => {
    URL.createObjectURL = vi.fn(() => 'blob:mock-url')
    URL.revokeObjectURL = vi.fn()
    renderWithI18n(<BackupCodesDisplay codes={['a1a1-a1a1']} username="alice" onDone={vi.fn()} />)

    fireEvent.click(screen.getByText('Als Datei speichern'))
    expect(URL.createObjectURL).toHaveBeenCalled()
  })
})
