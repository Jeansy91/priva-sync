import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, fireEvent, waitFor } from '@testing-library/react'
import { renderWithI18n } from '../../test/renderWithI18n'

const addSyncRoot = vi.fn()
const createServerSyncRoot = vi.fn()
const openDialog = vi.fn()

vi.mock('../../tauri', () => ({
  cmd: {
    addSyncRoot: (...a: unknown[]) => addSyncRoot(...a),
    createServerSyncRoot: (...a: unknown[]) => createServerSyncRoot(...a),
  },
}))

vi.mock('@tauri-apps/plugin-dialog', () => ({
  open: (...a: unknown[]) => openDialog(...a),
}))

import { AddSyncRootForm } from './AddSyncRootForm'

function renderForm(props: Partial<Parameters<typeof AddSyncRootForm>[0]> = {}) {
  return renderWithI18n(
    <AddSyncRootForm
      serverRoots={[]}
      supportsOnDemand={false}
      onCreated={vi.fn()}
      onClose={vi.fn()}
      onError={vi.fn()}
      onAdded={vi.fn()}
      {...props}
    />,
  )
}

beforeEach(() => {
  addSyncRoot.mockReset()
  createServerSyncRoot.mockReset()
  openDialog.mockReset()
})

describe('AddSyncRootForm (create-only)', () => {
  it('has no existing-folder dropdown', () => {
    renderForm()
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
    expect(screen.getByPlaceholderText('Name des neuen Ordners')).toBeInTheDocument()
  })

  it('creates a new server folder and adds it as a sync root', async () => {
    openDialog.mockResolvedValue('/local/New')
    createServerSyncRoot.mockResolvedValue({ id: 'n1', name: 'Projekte', path: '/Projekte' })
    addSyncRoot.mockResolvedValue(undefined)
    const onAdded = vi.fn()
    renderForm({ onAdded })

    fireEvent.change(screen.getByPlaceholderText('Name des neuen Ordners'), {
      target: { value: 'Projekte' },
    })
    fireEvent.click(screen.getByPlaceholderText('Ordner auswählen…'))
    await waitFor(() => expect(openDialog).toHaveBeenCalled())

    fireEvent.click(screen.getByRole('button', { name: 'Hinzufügen' }))

    await waitFor(() => expect(createServerSyncRoot).toHaveBeenCalledWith('Projekte'))
    expect(addSyncRoot).toHaveBeenCalledWith('n1', 'Projekte', '/local/New', 'n1', 'eager')
    expect(onAdded).toHaveBeenCalled()
  })
})
