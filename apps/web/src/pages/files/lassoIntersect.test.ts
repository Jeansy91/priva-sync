import { describe, expect, it } from 'vitest'
import { rectsIntersect, lassoRect, pathsIntersectingLasso } from './lassoIntersect'

describe('rectsIntersect', () => {
  it('returns true for overlapping rects', () => {
    expect(rectsIntersect({ x: 0, y: 0, w: 10, h: 10 }, { x: 5, y: 5, w: 10, h: 10 })).toBe(true)
  })
  it('returns false for non-overlapping rects', () => {
    expect(rectsIntersect({ x: 0, y: 0, w: 5, h: 5 }, { x: 10, y: 10, w: 5, h: 5 })).toBe(false)
  })
  it('touching edges count as intersecting', () => {
    expect(rectsIntersect({ x: 0, y: 0, w: 5, h: 5 }, { x: 5, y: 0, w: 5, h: 5 })).toBe(true)
  })
})

describe('lassoRect', () => {
  it('normalises drag from top-left to bottom-right', () => {
    expect(lassoRect({ x: 10, y: 20 }, { x: 30, y: 50 })).toEqual({ x: 10, y: 20, w: 20, h: 30 })
  })
  it('normalises drag from bottom-right to top-left', () => {
    expect(lassoRect({ x: 30, y: 50 }, { x: 10, y: 20 })).toEqual({ x: 10, y: 20, w: 20, h: 30 })
  })
})

describe('pathsIntersectingLasso', () => {
  it('returns rows whose rect intersects lasso', () => {
    const rows = [
      { path: '/a', rect: { x: 0, y: 0,  w: 100, h: 20 } },
      { path: '/b', rect: { x: 0, y: 20, w: 100, h: 20 } },
      { path: '/c', rect: { x: 0, y: 40, w: 100, h: 20 } },
    ]
    const hits = pathsIntersectingLasso(rows, { x: 5, y: 25, w: 10, h: 10 })
    expect(hits).toEqual(['/b'])
  })
})
