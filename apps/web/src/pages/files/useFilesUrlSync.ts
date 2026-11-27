import { useEffect } from 'react'
import type { SyncRoot, SharedMount } from '@privasync/api-client'

interface UseFilesUrlSyncInput {
  /** True when the URL points to a /files/mount/... share view. */
  mountMode:    boolean
  mountIDParam: string | null
  mountSubPath: string
  urlRootName:  string | null
  urlPath:      string
  syncRoots:    SyncRoot[]
  mounts:       SharedMount[]
  setSelectedRoot: (r: SyncRoot | null) => void
  setCurrentPath:  (p: string) => void
}

/**
 * Mirrors the URL into the page-level `selectedRoot` + `currentPath`
 * state. Three branches:
 *   - mount route → synthesises a SyncRoot facade from the matching mount
 *     so the rendering pipeline (Table/Grid/Detail) works unchanged
 *   - empty URL  → clears selection, drops the user back to /files
 *   - own root   → looks the root up by name and jumps to the path
 *
 * The mount facade lookup waits until the mount list has loaded; without
 * that, an SSE roundtrip would stall the mount view on a blank screen.
 */
export function useFilesUrlSync(input: UseFilesUrlSyncInput): void {
  const {
    mountMode, mountIDParam, mountSubPath, urlRootName, urlPath,
    syncRoots, mounts, setSelectedRoot, setCurrentPath,
  } = input

  useEffect(() => {
    // Mount mode: synthesise a SyncRoot facade from the matching mount so
    // the existing rendering pipeline (Table/Grid/Detail) works unchanged.
    // The id stays the share-id; loadFiles dispatches on that id to route
    // to the mount endpoint. Write-actions remain wired to own-root
    // endpoints and will fail server-side for mounts — handled by later
    // slices that gate those buttons.
    if (mountMode) {
      if (!mountIDParam || mounts.length === 0) return
      const m = mounts.find((x) => x.id === mountIDParam)
      if (!m) return
      const facade: SyncRoot = {
        id:         m.id,
        user_id:    m.owner.id,
        name:       m.name,
        path:       m.path,
        created_at: '',
        owner:      m.owner,
      }
      setSelectedRoot(facade)
      setCurrentPath(mountSubPath)
      return
    }
    // Non-mount mode. Clear before the roots-list check so we leave the
    // mount-facade behind when SSE kicked us out of a revoked mount and
    // syncRoots may still be empty.
    if (!urlRootName) {
      setSelectedRoot(null)
      setCurrentPath('/')
      return
    }
    if (syncRoots.length === 0) return
    const root = syncRoots.find(r => r.name === urlRootName)
    if (root) {
      setSelectedRoot(root)
      setCurrentPath(urlPath)
    }
  }, [
    mountMode, mountIDParam, mountSubPath, urlRootName, urlPath,
    syncRoots, mounts, setSelectedRoot, setCurrentPath,
  ])
}
