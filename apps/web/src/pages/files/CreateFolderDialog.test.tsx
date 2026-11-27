import { describe, it, expect, vi } from 'vitest'
import { fireEvent } from '@testing-library/react'
import { renderWithI18n } from '../../test/renderWithI18n'
import { CreateFolderDialog } from './CreateFolderDialog'

describe('CreateFolderDialog', () => {
  it('verbotene Zeichen lösen Fehler aus und blockieren onConfirm', () => {
    const onConfirm = vi.fn()
    const { getByRole, getByPlaceholderText, getByText } = renderWithI18n(
      <CreateFolderDialog
        open
        parentPath="/"
        existingNames={[]}
        loading={false}
        serverError={null}
        onConfirm={onConfirm}
        onClose={() => {}}
      />,
      { lang: 'de' },
    )
    const input = getByPlaceholderText('Ordnername')
    fireEvent.change(input, { target: { value: 'foo/bar' } })
    fireEvent.click(getByRole('button', { name: /Erstellen/ }))
    expect(getByText(/Ungültige Zeichen/)).toBeTruthy()
    expect(onConfirm).not.toHaveBeenCalled()
  })

  it('doppelter Name zeigt Fehler und blockiert onConfirm', () => {
    const onConfirm = vi.fn()
    const { getByRole, getByPlaceholderText, getByText } = renderWithI18n(
      <CreateFolderDialog
        open
        parentPath="/parent"
        existingNames={['Bilder', 'Dokumente']}
        loading={false}
        serverError={null}
        onConfirm={onConfirm}
        onClose={() => {}}
      />,
      { lang: 'de' },
    )
    fireEvent.change(getByPlaceholderText('Ordnername'), { target: { value: 'Bilder' } })
    fireEvent.click(getByRole('button', { name: /Erstellen/ }))
    expect(getByText(/bereits/)).toBeTruthy()
    expect(onConfirm).not.toHaveBeenCalled()
  })

  it('gültiger Name ruft onConfirm mit getrimmtem Wert', () => {
    const onConfirm = vi.fn()
    const { getByRole, getByPlaceholderText } = renderWithI18n(
      <CreateFolderDialog
        open
        parentPath="/parent"
        existingNames={[]}
        loading={false}
        serverError={null}
        onConfirm={onConfirm}
        onClose={() => {}}
      />,
      { lang: 'de' },
    )
    fireEvent.change(getByPlaceholderText('Ordnername'), { target: { value: '  Neuer Ordner  ' } })
    fireEvent.click(getByRole('button', { name: /Erstellen/ }))
    expect(onConfirm).toHaveBeenCalledWith('Neuer Ordner')
  })

  it('Confirm-Button ist disabled bei leerem Namen', () => {
    const onConfirm = vi.fn()
    const { getByRole } = renderWithI18n(
      <CreateFolderDialog
        open
        parentPath="/"
        existingNames={[]}
        loading={false}
        serverError={null}
        onConfirm={onConfirm}
        onClose={() => {}}
      />,
      { lang: 'de' },
    )
    const btn = getByRole('button', { name: /Erstellen/ }) as HTMLButtonElement
    expect(btn.disabled).toBe(true)
    fireEvent.click(btn)
    expect(onConfirm).not.toHaveBeenCalled()
  })
})
