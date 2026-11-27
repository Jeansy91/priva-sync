import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor, act } from '@testing-library/react'
import { renderWithI18n } from '../test/renderWithI18n'

let transferListener: ((e: { upload_bps: number; download_bps: number }) => void) | undefined

vi.mock('../tauri', () => ({
  onTransferSpeed: (cb: (e: { upload_bps: number; download_bps: number }) => void) => {
    transferListener = cb
    return Promise.resolve(() => {})
  },
}))

import TransferSpeedBar, { advance, newSmoother } from './TransferSpeedBar'
import { useAppStore } from '../store'

beforeEach(() => {
  transferListener = undefined
  useAppStore.setState({ status: null } as any)
})

describe('TransferSpeedBar', () => {
  it('shows the idle status label by default', () => {
    renderWithI18n(<TransferSpeedBar />)
    expect(screen.getByText('Synchronisiert')).toBeInTheDocument()
    expect(screen.getAllByText('0 B/s')).toHaveLength(2)
  })

  it('reflects the store status and pending count', () => {
    useAppStore.setState({ status: { status: 'syncing', pending_count: 3 } } as any)
    renderWithI18n(<TransferSpeedBar />)
    expect(screen.getByText('Synchronisiert…')).toBeInTheDocument()
    expect(screen.getByText('(3 ausstehend)')).toBeInTheDocument()
  })

  it('formats incoming transfer-speed events', async () => {
    renderWithI18n(<TransferSpeedBar />)
    await waitFor(() => expect(transferListener).toBeDefined())

    // EMA smoothing (alpha 0.3) applies to raw bps before formatting, so the
    // raw input must clear the next unit's threshold divided by 0.3.
    act(() => {
      transferListener!({ upload_bps: 4_000, download_bps: 4 * 1024 * 1024 })
    })

    await waitFor(() => {
      expect(screen.getByText(/KB\/s/)).toBeInTheDocument()
      expect(screen.getByText(/MB\/s/)).toBeInTheDocument()
    })
  })

  it('rides out a single empty sample instead of resetting to zero', async () => {
    renderWithI18n(<TransferSpeedBar />)
    await waitFor(() => expect(transferListener).toBeDefined())

    act(() => transferListener!({ upload_bps: 40_000, download_bps: 0 }))
    await waitFor(() => expect(screen.getByText(/KB\/s/)).toBeInTheDocument())

    // Chunked transfers credit bytes in bursts, so an idle second mid-transfer
    // is normal — the reading decays rather than snapping to zero.
    act(() => transferListener!({ upload_bps: 0, download_bps: 0 }))
    await waitFor(() => expect(screen.getByText(/KB\/s/)).toBeInTheDocument())
  })

  it('drops a direction to 0 B/s after a run of empty samples', async () => {
    renderWithI18n(<TransferSpeedBar />)
    await waitFor(() => expect(transferListener).toBeDefined())

    act(() => transferListener!({ upload_bps: 40_000, download_bps: 0 }))
    await waitFor(() => expect(screen.getByText(/KB\/s/)).toBeInTheDocument())

    for (let i = 0; i < 3; i++) {
      act(() => transferListener!({ upload_bps: 0, download_bps: 0 }))
    }
    await waitFor(() => {
      expect(screen.getAllByText('0 B/s')).toHaveLength(2)
    })
  })
})

describe('advance', () => {
  it('smooths towards the raw rate instead of jumping to it', () => {
    const s = newSmoother()
    expect(advance(s, 1_000)).toBeCloseTo(300)
    expect(advance(s, 1_000)).toBeCloseTo(510)
  })

  it('decays over an empty sample but keeps a reading', () => {
    const s = newSmoother()
    advance(s, 1_000)
    expect(advance(s, 0)).toBeCloseTo(210)
  })

  it('reaches exactly zero after three consecutive empty samples', () => {
    const s = newSmoother()
    advance(s, 1_000)
    advance(s, 0)
    advance(s, 0)
    expect(advance(s, 0)).toBe(0)
  })

  it('restarts smoothing from zero once traffic resumes', () => {
    const s = newSmoother()
    advance(s, 1_000)
    for (let i = 0; i < 3; i++) advance(s, 0)
    expect(advance(s, 1_000)).toBeCloseTo(300)
  })
})
