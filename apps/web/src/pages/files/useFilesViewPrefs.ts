import { useState, useEffect } from 'react'
import type { FilesViewMode } from './filesViewTypes'
import { DETAILS_VISIBLE_KEY, VIEW_MODE_KEY, readStoredViewMode } from './filesPagePrefs'

export interface FilesViewPrefs {
  detailsVisible:    boolean
  setDetailsVisible: (v: boolean | ((prev: boolean) => boolean)) => void
  viewMode:          FilesViewMode
  setViewMode:       (m: FilesViewMode | ((prev: FilesViewMode) => FilesViewMode)) => void
}

/**
 * Persists the user's view-mode (`table`/`grid`) and detail-panel visibility
 * in localStorage so the choice survives navigation and reloads.
 */
export function useFilesViewPrefs(): FilesViewPrefs {
  const [detailsVisible, setDetailsVisible] = useState<boolean>(() => {
    if (typeof window === 'undefined') return true
    const v = window.localStorage.getItem(DETAILS_VISIBLE_KEY)
    return v === null ? true : v === '1'
  })
  useEffect(() => {
    window.localStorage.setItem(DETAILS_VISIBLE_KEY, detailsVisible ? '1' : '0')
  }, [detailsVisible])

  const [viewMode, setViewMode] = useState<FilesViewMode>(readStoredViewMode)
  useEffect(() => {
    window.localStorage.setItem(VIEW_MODE_KEY, viewMode)
  }, [viewMode])

  return { detailsVisible, setDetailsVisible, viewMode, setViewMode }
}
