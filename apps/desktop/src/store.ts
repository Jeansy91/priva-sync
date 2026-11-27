/**
 * Global Zustand store for desktop app state.
 */
import { create } from "zustand";
import type { ClientConfig, SyncStatus, SyncRootEntry, SyncProgressEvent, PathWarning } from "./tauri";

interface AppState {
  config:       ClientConfig | null;
  status:       SyncStatus   | null;
  syncRoots:    SyncRootEntry[];
  // Progress per sync-root id, nested by file path — lets us surface parallel
  // uploads/downloads individually. An entry lives until the file_done
  // event (per file) or the done event (whole root) arrives.
  rootProgress: Record<string, Record<string, SyncProgressEvent>>;
  // Path warnings emitted by the sync engine (cleared on app restart)
  pathWarnings: PathWarning[];

  setConfig:          (c: ClientConfig)      => void;
  setStatus:          (s: SyncStatus)        => void;
  setSyncRoots:       (r: SyncRootEntry[])   => void;
  setRootProgress:    (e: SyncProgressEvent) => void;
  addPathWarning:     (w: PathWarning)       => void;
  dismissPathWarning: (path: string)         => void;
}

export const useAppStore = create<AppState>((set) => ({
  config:       null,
  status:       null,
  syncRoots:    [],
  rootProgress: {},
  pathWarnings: [],

  setConfig:    (config)    => set({ config }),
  setStatus:    (status)    => set({ status }),
  setSyncRoots: (syncRoots) => set({ syncRoots }),

  setRootProgress: (e) => set((state) => {
    // Root-level done: alle aktiven Slots clearen.
    if (e.action === "done") {
      const next = { ...state.rootProgress };
      delete next[e.root_id];
      return { rootProgress: next };
    }
    const rootMap = { ...(state.rootProgress[e.root_id] ?? {}) };
    // Per-file done: einzelnen Slot entfernen.
    if (e.action === "file_done") {
      delete rootMap[e.file];
    } else {
      rootMap[e.file] = e;
    }
    const next = { ...state.rootProgress };
    if (Object.keys(rootMap).length === 0) {
      delete next[e.root_id];
    } else {
      next[e.root_id] = rootMap;
    }
    return { rootProgress: next };
  }),

  addPathWarning: (w) => set((state) => ({
    pathWarnings: state.pathWarnings.some((x) => x.path === w.path)
      ? state.pathWarnings
      : [...state.pathWarnings, w],
  })),

  dismissPathWarning: (path) => set((state) => ({
    pathWarnings: state.pathWarnings.filter((w) => w.path !== path),
  })),
}));
