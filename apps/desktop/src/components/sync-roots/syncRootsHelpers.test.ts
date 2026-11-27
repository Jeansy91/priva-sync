import { describe, it, expect } from 'vitest'
import { formatBytes } from './syncRootsHelpers'

describe('formatBytes', () => {
  it('renders sub-KB values in bytes', () => {
    expect(formatBytes(0)).toBe('0 B')
    expect(formatBytes(512)).toBe('512 B')
    expect(formatBytes(1023)).toBe('1023 B')
  })

  it('renders KB with one decimal below 10 and none above', () => {
    expect(formatBytes(1024)).toBe('1.0 KB')
    expect(formatBytes(1536)).toBe('1.5 KB')
    expect(formatBytes(10 * 1024)).toBe('10 KB')
  })

  it('escalates through MB/GB/TB', () => {
    expect(formatBytes(1024 * 1024)).toBe('1.0 MB')
    expect(formatBytes(1024 * 1024 * 1024)).toBe('1.0 GB')
    expect(formatBytes(1024 * 1024 * 1024 * 1024)).toBe('1.0 TB')
  })

  it('caps at TB even for values beyond it', () => {
    expect(formatBytes(1024 * 1024 * 1024 * 1024 * 1024)).toBe('1024 TB')
  })
})
