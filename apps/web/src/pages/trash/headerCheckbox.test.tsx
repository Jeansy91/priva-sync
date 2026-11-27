import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { HeaderCheckbox } from './TrashTree'

// Harness mirrors TrashListTable/TrashTree wiring: onToggle(check) replaces the
// whole selection with all-or-nothing.
function Harness({ total, initial }: { total: number; initial: number }) {
  const [count, setCount] = useState(initial)
  return (
    <>
      <span data-testid="count">{count}</span>
      <HeaderCheckbox
        total={total}
        selectedCount={count}
        label="all"
        onToggle={(check) => setCount(check ? total : 0)}
      />
    </>
  )
}

describe('HeaderCheckbox master deselect', () => {
  it('selects all when nothing is selected', async () => {
    render(<Harness total={3} initial={0} />)
    await userEvent.click(screen.getByLabelText('all'))
    expect(screen.getByTestId('count').textContent).toBe('3')
  })

  it('clears when all are selected', async () => {
    render(<Harness total={3} initial={3} />)
    await userEvent.click(screen.getByLabelText('all'))
    expect(screen.getByTestId('count').textContent).toBe('0')
  })

  // A partial (indeterminate) box first fills to a full selection; only a
  // second click then clears. Intent is derived from `allChecked`, not from the
  // native toggled value (the browser sets `checked=true` on an indeterminate
  // box, which would otherwise make the action unreliable).
  it('selects all from a partial (indeterminate) selection', async () => {
    render(<Harness total={3} initial={2} />)
    await userEvent.click(screen.getByLabelText('all'))
    expect(screen.getByTestId('count').textContent).toBe('3')
  })
})
