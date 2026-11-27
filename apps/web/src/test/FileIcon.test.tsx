import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'
import '@testing-library/jest-dom/vitest'
import { FileIcon } from '@privasync/ui'

describe('<FileIcon>', () => {
  it('renders with data-file-kind for known type', () => {
    const { container } = render(<FileIcon name="a.png" />)
    expect(container.firstChild).toHaveAttribute('data-file-kind', 'image')
  })

  it('renders unknown kind for mystery extension', () => {
    const { container } = render(<FileIcon name="x.zzz" />)
    expect(container.firstChild).toHaveAttribute('data-file-kind', 'unknown')
  })

  it('applies provided className', () => {
    const { container } = render(<FileIcon name="a.pdf" className="h-4 w-4" />)
    expect(container.firstChild).toHaveClass('h-4', 'w-4')
  })

  it('renders office brand kinds for word/excel/ppt', () => {
    const { container: w } = render(<FileIcon name="a.docx" />)
    expect(w.firstChild).toHaveAttribute('data-file-kind', 'word')
    const { container: e } = render(<FileIcon name="a.xlsx" />)
    expect(e.firstChild).toHaveAttribute('data-file-kind', 'excel')
    const { container: p } = render(<FileIcon name="a.pptx" />)
    expect(p.firstChild).toHaveAttribute('data-file-kind', 'ppt')
  })
})
