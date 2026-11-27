import { useState, useEffect, useCallback, useMemo, useRef } from 'react'
import type { SyncRoot, FileEntry, SharedMount } from '@privasync/api-client'
import { apiClient } from '../../store/auth'
import type { SearchHit } from './searchEntries'
import type { GlobalSearchGroup } from './FilesGlobalSearchResults'

interface UseFilesSearchInput {
  /** True when the user has a non-empty query in the URL. */
  searchActive: boolean
  selectedRoot: SyncRoot | null
  currentPath:  string
  /** Live query from the URL (`?q=`). */
  searchQuery:  string
  /** Same as searchQuery but pulled separately so input-resync works during URL pops. */
  urlQuery:     string
  /** Drives the mount lookup inside global-search group hydration. */
  mounts:       SharedMount[]
  /** Localized error string getter. */
  t: (key: string) => string
  /** From react-router; the hook resets `?q=` on navigation. */
  setUrlParams: (
    nextInit: (prev: URLSearchParams) => URLSearchParams,
    navigateOptions?: { replace?: boolean },
  ) => void
}

/**
 * Owns the local + global file-search state for FilesPage. Mirrors the
 * URL `?q=` parameter, fetches per-root and cross-root results, and
 * exposes the imperative cursor-pagination callbacks the views need.
 */
export function useFilesSearch(input: UseFilesSearchInput) {
  const { searchActive, selectedRoot, currentPath, searchQuery, urlQuery, mounts, t, setUrlParams } = input

  const [searchInput, setSearchInput]             = useState(urlQuery)
  const [searchResults, setSearchResults]         = useState<FileEntry[]>([])
  const [searchNextCursor, setSearchNextCursor]   = useState('')
  const [searchLoading, setSearchLoading]         = useState(false)
  const [searchError, setSearchError]             = useState<string | null>(null)

  // Loads the first search page, replacing any prior hits.
  useEffect(() => {
    if (!searchActive || !selectedRoot) {
      setSearchResults([])
      setSearchNextCursor('')
      setSearchError(null)
      return
    }
    let cancelled = false
    setSearchLoading(true)
    setSearchError(null)
    apiClient.files
      .search(selectedRoot.id, searchQuery, { path: currentPath })
      .then((res) => {
        if (cancelled) return
        setSearchResults(res.files ?? [])
        setSearchNextCursor(res.next_cursor ?? '')
      })
      .catch(() => {
        if (cancelled) return
        setSearchError(t('files.loadFilesFailed'))
        setSearchResults([])
        setSearchNextCursor('')
      })
      .finally(() => { if (!cancelled) setSearchLoading(false) })
    return () => { cancelled = true }
  }, [searchActive, selectedRoot, currentPath, searchQuery, t])

  // Global cross-root search: only fires when no sync-root is selected.
  // Results are grouped by root; per-group load-more reuses the per-root
  // search endpoint and appends into the matching group.
  const [globalGroups, setGlobalGroups]   = useState<GlobalSearchGroup[]>([])
  const [globalLoading, setGlobalLoading] = useState(false)
  const [globalError, setGlobalError]     = useState<string | null>(null)

  useEffect(() => {
    if (!searchActive || selectedRoot) {
      setGlobalGroups([])
      setGlobalError(null)
      return
    }
    let cancelled = false
    setGlobalLoading(true)
    setGlobalError(null)
    apiClient.files
      .searchGlobal(searchQuery)
      .then((res) => {
        if (cancelled) return
        setGlobalGroups((res.groups ?? []).map((g) => {
          const mountID = (g as { mount_id?: string }).mount_id
          return {
            rootID:     g.sync_root_id,
            rootName:   g.sync_root_name,
            mountID,
            mount:      mountID ? mounts.find((m) => m.id === mountID) : undefined,
            files:      g.files ?? [],
            nextCursor: g.next_cursor ?? '',
            loading:    false,
          }
        }))
      })
      .catch(() => {
        if (cancelled) return
        setGlobalError(t('files.loadFilesFailed'))
        setGlobalGroups([])
      })
      .finally(() => { if (!cancelled) setGlobalLoading(false) })
    return () => { cancelled = true }
  }, [searchActive, selectedRoot, searchQuery, mounts, t])

  const loadMoreGlobalGroup = useCallback(async (rootID: string) => {
    const group = globalGroups.find((g) => g.rootID === rootID)
    if (!group || !group.nextCursor || group.loading) return
    setGlobalGroups((prev) => prev.map((g) =>
      g.rootID === rootID ? { ...g, loading: true } : g,
    ))
    try {
      const res = await apiClient.files.search(rootID, searchQuery, {
        cursor: group.nextCursor,
      })
      setGlobalGroups((prev) => prev.map((g) =>
        g.rootID === rootID
          ? { ...g, files: g.files.concat(res.files ?? []), nextCursor: res.next_cursor ?? '', loading: false }
          : g,
      ))
    } catch {
      setGlobalGroups((prev) => prev.map((g) =>
        g.rootID === rootID ? { ...g, loading: false } : g,
      ))
      setGlobalError(t('files.loadFilesFailed'))
    }
  }, [globalGroups, searchQuery, t])

  const loadMoreSearch = useCallback(async () => {
    if (!selectedRoot || !searchNextCursor || searchLoading) return
    setSearchLoading(true)
    try {
      const res = await apiClient.files.search(selectedRoot.id, searchQuery, {
        path:   currentPath,
        cursor: searchNextCursor,
      })
      setSearchResults((prev) => prev.concat(res.files ?? []))
      setSearchNextCursor(res.next_cursor ?? '')
    } catch {
      setSearchError(t('files.loadFilesFailed'))
    } finally {
      setSearchLoading(false)
    }
  }, [selectedRoot, searchNextCursor, searchLoading, searchQuery, currentPath, t])

  const searchHits = useMemo<SearchHit[]>(() => {
    const hits: SearchHit[] = searchResults.map((f) => {
      const slash = f.path.lastIndexOf('/')
      const name  = slash < 0 ? f.path : f.path.slice(slash + 1)
      return f.is_dir
        ? { kind: 'dir',  name, path: f.path, relativeDir: '' }
        : { kind: 'file', name, file: f,     relativeDir: '' }
    })
    return hits.sort((a, b) => {
      if (a.kind !== b.kind) return a.kind === 'dir' ? -1 : 1
      return a.name.localeCompare(b.name)
    })
  }, [searchResults])

  // Reset on directory/root change. Guarded by value comparison: the
  // react-router setter changes identity on every URL change (committing
  // `?q=` included), and the mount facade is a fresh object per effect run —
  // resetting on those would clear the query right after it was set.
  const selectedRootID = selectedRoot?.id ?? null
  const prevLocation = useRef({ rootID: selectedRootID, path: currentPath })
  useEffect(() => {
    if (prevLocation.current.rootID === selectedRootID && prevLocation.current.path === currentPath) return
    prevLocation.current = { rootID: selectedRootID, path: currentPath }
    setSearchInput('')
    setUrlParams((prev) => {
      const next = new URLSearchParams(prev)
      next.delete('q')
      return next
    }, { replace: true })
  }, [selectedRootID, currentPath, setUrlParams])

  // External URL change (e.g. back button) — keep the input field in sync.
  useEffect(() => { setSearchInput(urlQuery) }, [urlQuery])

  const commitSearch = useCallback(() => {
    setUrlParams((prev) => {
      const next = new URLSearchParams(prev)
      const q = searchInput.trim()
      if (q === '') next.delete('q')
      else next.set('q', q)
      return next
    })
  }, [searchInput, setUrlParams])

  const clearSearch = useCallback(() => {
    setSearchInput('')
    setUrlParams((prev) => {
      const next = new URLSearchParams(prev)
      next.delete('q')
      return next
    })
  }, [setUrlParams])

  return {
    searchInput, setSearchInput,
    searchHits,
    searchNextCursor, searchLoading, searchError,
    globalGroups, globalLoading, globalError,
    loadMoreSearch, loadMoreGlobalGroup,
    commitSearch, clearSearch,
  }
}
