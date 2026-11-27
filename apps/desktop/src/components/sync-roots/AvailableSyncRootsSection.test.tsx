import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, fireEvent, waitFor } from '@testing-library/react'
import { renderWithI18n } from '../../test/renderWithI18n'

const addSyncRoot = vi.fn()
const openDialog = vi.fn()

vi.mock('../../tauri', () => ({
  cmd: {
    addSyncRoot: (...args: unknown[]) => addSyncRoot(...args),
  },
}))

vi.mock('@tauri-apps/plugin-dialog', () => ({
  open: (...args: unknown[]) => openDialog(...args),
}))

import { AvailableSyncRootsSection } from './AvailableSyncRootsSection'

const roots = [
  { id: 'r1', name: 'Fotos', path: '/Fotos' },
  { id: 'r2', name: 'Belege', path: '/Belege' },
]

function renderSection(props: Partial<Parameters<typeof AvailableSyncRootsSection>[0]> = {}) {
  return renderWithI18n(
    <AvailableSyncRootsSection
      addableRoots={roots}
      supportsOnDemand={true}
      onAdded={vi.fn()}
      onError={vi.fn()}
      {...props}
    />,
  )
}

beforeEach(() => {
  addSyncRoot.mockReset()
  openDialog.mockReset()
})

describe('AvailableSyncRootsSection', () => {
  it('renders one add button per addable server root', () => {
    renderSection()
    expect(screen.getByText('Fotos')).toBeInTheDocument()
    expect(screen.getByText('Belege')).toBeInTheDocument()
    expect(screen.getAllByRole('button', { name: 'Hinzufügen' })).toHaveLength(2)
  })

  it('renders nothing when there are no addable roots', () => {
    const { container } = renderSection({ addableRoots: [] })
    expect(container).toBeEmptyDOMElement()
  })

  it('adds the picked root after the folder dialog returns a path', async () => {
    openDialog.mockResolvedValue('/local/Fotos')
    addSyncRoot.mockResolvedValue(undefined)
    const onAdded = vi.fn()
    renderSection({ onAdded })

    fireEvent.click(screen.getAllByRole('button', { name: 'Hinzufügen' })[0])

    await waitFor(() =>
      expect(addSyncRoot).toHaveBeenCalledWith('r1', 'Fotos', '/local/Fotos', 'r1', 'on_demand'),
    )
    expect(onAdded).toHaveBeenCalled()
  })

  it('does not add when the folder dialog is cancelled', async () => {
    openDialog.mockResolvedValue(null)
    const onAdded = vi.fn()
    renderSection({ onAdded })

    fireEvent.click(screen.getAllByRole('button', { name: 'Hinzufügen' })[0])

    await waitFor(() => expect(openDialog).toHaveBeenCalled())
    expect(addSyncRoot).not.toHaveBeenCalled()
    expect(onAdded).not.toHaveBeenCalled()
  })

  it('uses eager download mode when the platform has no placeholder support', async () => {
    openDialog.mockResolvedValue('/local/Fotos')
    addSyncRoot.mockResolvedValue(undefined)
    renderSection({ supportsOnDemand: false })

    fireEvent.click(screen.getAllByRole('button', { name: 'Hinzufügen' })[0])

    await waitFor(() =>
      expect(addSyncRoot).toHaveBeenCalledWith('r1', 'Fotos', '/local/Fotos', 'r1', 'eager'),
    )
  })

  it('reports an error when adding the root fails', async () => {
    openDialog.mockResolvedValue('/local/Fotos')
    addSyncRoot.mockRejectedValue('boom')
    const onError = vi.fn()
    renderSection({ onError })

    fireEvent.click(screen.getAllByRole('button', { name: 'Hinzufügen' })[0])

    await waitFor(() => expect(onError).toHaveBeenCalledWith('boom'))
  })
})
