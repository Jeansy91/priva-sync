/**
 * Compact byte formatter for the admin user table. Picks the largest
 * unit where the value is still >= 1 and trims trailing zeros from the
 * tenth.
 */
export function formatBytes(b: number): string {
  if (b === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(b) / Math.log(k))
  return `${parseFloat((b / Math.pow(k, i)).toFixed(1))} ${sizes[i]}`
}
