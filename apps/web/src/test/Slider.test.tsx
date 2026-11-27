import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { Slider } from '@privasync/ui'

describe('Slider', () => {
  it('spiegelt den value auf den Range-Input', () => {
    render(<Slider value={3} min={1} max={8} onChange={() => {}} />)
    const input = screen.getByRole('slider') as HTMLInputElement
    expect(input.value).toBe('3')
  })

  it('ruft onChange mit numerischem Wert auf, wenn der Range-Input bewegt wird', () => {
    const onChange = vi.fn()
    render(<Slider value={3} min={1} max={8} onChange={onChange} />)
    const input = screen.getByRole('slider') as HTMLInputElement
    fireEvent.change(input, { target: { value: '5' } })
    expect(onChange).toHaveBeenCalledWith(5)
  })

  it('rendert einen Tick-Marker pro Schritt im Bereich', () => {
    const { container } = render(<Slider value={1} min={1} max={4} onChange={() => {}} hideTicks={false} />)
    const ticks = container.querySelectorAll('div.flex.justify-between > span')
    expect(ticks).toHaveLength(4)
    expect(Array.from(ticks).map((t) => t.textContent)).toEqual(['1', '2', '3', '4'])
  })

  it('zeigt keine Ticks bei hideTicks', () => {
    const { container } = render(<Slider value={1} min={1} max={4} onChange={() => {}} hideTicks />)
    const ticks = container.querySelectorAll('div.flex.justify-between > span')
    expect(ticks).toHaveLength(0)
  })
})
