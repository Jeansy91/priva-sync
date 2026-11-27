export interface Rect { x: number; y: number; w: number; h: number }
export interface Point { x: number; y: number }
export interface RowRect { path: string; rect: Rect }

export function rectsIntersect(a: Rect, b: Rect): boolean {
  return a.x <= b.x + b.w && a.x + a.w >= b.x
      && a.y <= b.y + b.h && a.y + a.h >= b.y
}

export function lassoRect(start: Point, end: Point): Rect {
  const x = Math.min(start.x, end.x)
  const y = Math.min(start.y, end.y)
  return { x, y, w: Math.abs(end.x - start.x), h: Math.abs(end.y - start.y) }
}

export function pathsIntersectingLasso(rows: RowRect[], lasso: Rect): string[] {
  const out: string[] = []
  for (const r of rows) {
    if (rectsIntersect(lasso, r.rect)) out.push(r.path)
  }
  return out
}
