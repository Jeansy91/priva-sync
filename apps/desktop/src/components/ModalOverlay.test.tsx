import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import ModalOverlay from './ModalOverlay'

describe('ModalOverlay', () => {
  it('renders children into document.body via a portal', () => {
    render(
      <ModalOverlay>
        <div data-testid="modal-content">hello</div>
      </ModalOverlay>,
    )
    expect(screen.getByTestId('modal-content')).toBeInTheDocument()
    expect(document.body.style.overflow).toBe('hidden')
  })

  it('calls onDismiss when the backdrop itself is clicked', () => {
    const onDismiss = vi.fn()
    render(
      <ModalOverlay onDismiss={onDismiss}>
        <div data-testid="modal-content">hello</div>
      </ModalOverlay>,
    )
    fireEvent.click(screen.getByTestId('modal-content').parentElement!)
    expect(onDismiss).toHaveBeenCalled()
  })

  it('does not call onDismiss when a child is clicked', () => {
    const onDismiss = vi.fn()
    render(
      <ModalOverlay onDismiss={onDismiss}>
        <div data-testid="modal-content">hello</div>
      </ModalOverlay>,
    )
    fireEvent.click(screen.getByTestId('modal-content'))
    expect(onDismiss).not.toHaveBeenCalled()
  })

  it('restores the previous body overflow style on unmount', () => {
    document.body.style.overflow = 'auto'
    const { unmount } = render(
      <ModalOverlay>
        <div>content</div>
      </ModalOverlay>,
    )
    expect(document.body.style.overflow).toBe('hidden')
    unmount()
    expect(document.body.style.overflow).toBe('auto')
  })
})
