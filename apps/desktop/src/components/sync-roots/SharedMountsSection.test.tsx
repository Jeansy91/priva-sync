import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { renderWithI18n } from '../../test/renderWithI18n'
import type { SyncRootEntry, AvailableMount } from '../../tauri'

const listAvailableMounts = vi.fn()
const activateMount = vi.fn()
const openDialog = vi.fn()

vi.mock('../../tauri', () => ({
  cmd: {
    listAvailableMounts: (...a: unknown[]) => listAvailableMounts(...a),
    activateMount: (...a: unknown[]) => activateMount(...a),
  },
  onSharesChanged: () => Promise.resolve(() => {}),
}))

vi.mock('@tauri-apps/plugin-dialog', () => ({
  open: (...a: unknown[]) => openDialog(...a),
}))

import { SharedMountsSection } from './SharedMountsSection'

function mountRoot(over: Partial<SyncRootEntry> = {}): SyncRootEntry {
  return {
    id: 'm1', name: 'AlicePics', local_path: '/local/AlicePics', server_root_id: 's-alice',
    download_mode: 'eager', read_only: false, paused: false, kind: 'mount', ...over,
  }
}

const available: AvailableMount = {
  share_id: 's-bob', name: 'BobDocs', path: '/Docs', can_write: true, owner_username: 'bob',
}

function renderSection(props: Partial<Parameters<typeof SharedMountsSection>[0]> = {}) {
  return renderWithI18n(
    <SharedMountsSection
      activeMounts={[mountRoot()]}
      progress={{}}
      supportsOnDemand={true}
      onToggleDownloadMode={vi.fn()}
      onTogglePaused={vi.fn()}
      onRemove={vi.fn()}
      onActivated={vi.fn()}
      onError={vi.fn()}
      {...props}
    />,
  )
}

beforeEach(() => {
  listAvailableMounts.mockReset().mockResolvedValue([])
  activateMount.mockReset()
  openDialog.mockReset()
})

describe('SharedMountsSection (mount group)', () => {
  it('lists active mounts above available ones under a sub-heading', async () => {
    listAvailableMounts.mockResolvedValue([available])
    renderSection()

    expect(screen.getByText('Mit mir geteilt')).toBeInTheDocument()
    expect(screen.getByText('AlicePics')).toBeInTheDocument()
    await screen.findByText('BobDocs')
    expect(screen.getByText('Verfügbare Freigaben')).toBeInTheDocument()

    // Active mount must come before the available one in the DOM.
    const active = screen.getByText('AlicePics')
    const avail = screen.getByText('BobDocs')
    expect(active.compareDocumentPosition(avail) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('renders nothing when there are no active and no available mounts', async () => {
    const { container } = renderSection({ activeMounts: [] })
    await waitFor(() => expect(listAvailableMounts).toHaveBeenCalled())
    expect(container).toBeEmptyDOMElement()
  })

  it('omits the available sub-heading when there are no available mounts', async () => {
    renderSection()
    await waitFor(() => expect(listAvailableMounts).toHaveBeenCalled())
    expect(screen.getByText('AlicePics')).toBeInTheDocument()
    expect(screen.queryByText('Verfügbare Freigaben')).not.toBeInTheDocument()
  })
})
