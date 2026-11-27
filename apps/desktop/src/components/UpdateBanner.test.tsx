import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, fireEvent, waitFor } from '@testing-library/react'
import { renderWithI18n } from '../test/renderWithI18n'

const installUpdate = vi.fn()

vi.mock('../tauri', () => ({
  cmd: {
    installUpdate: (...a: unknown[]) => installUpdate(...a),
  },
}))

import UpdateBanner from './UpdateBanner'

beforeEach(() => {
  installUpdate.mockReset()
})

describe('UpdateBanner', () => {
  const info = { version: '1.2.0', current: '1.1.0', notes: 'Bugfixes', release_date: '2026-08-01' }

  it('shows version, current version and notes', () => {
    renderWithI18n(<UpdateBanner info={info} />)
    expect(screen.getByText(/1\.2\.0/)).toBeInTheDocument()
    expect(screen.getByText(/1\.1\.0/)).toBeInTheDocument()
    expect(screen.getByText('Bugfixes')).toBeInTheDocument()
  })

  it('installs the update on click', async () => {
    installUpdate.mockResolvedValue(undefined)
    renderWithI18n(<UpdateBanner info={info} />)

    fireEvent.click(screen.getByText('Jetzt installieren'))

    await waitFor(() => expect(installUpdate).toHaveBeenCalled())
  })

  it('shows an error and re-enables the button when install fails', async () => {
    installUpdate.mockRejectedValue(new Error('network down'))
    renderWithI18n(<UpdateBanner info={info} />)

    fireEvent.click(screen.getByText('Jetzt installieren'))

    await waitFor(() => expect(screen.getByText(/network down/)).toBeInTheDocument())
    expect(screen.getByText('Jetzt installieren')).not.toBeDisabled()
  })

  it('offers no way to dismiss the update', () => {
    renderWithI18n(<UpdateBanner info={info} />)

    expect(screen.getAllByRole('button')).toHaveLength(1)
    expect(screen.getByRole('button', { name: 'Jetzt installieren' })).toBeInTheDocument()
  })
})
