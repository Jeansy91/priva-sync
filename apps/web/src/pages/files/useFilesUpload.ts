import { useState, useRef, useCallback } from 'react'
import type { SyncRoot } from '@privasync/api-client'
import { apiClient } from '../../store/auth'
import type { UploadItem } from './UploadQueue'
import {
  type UploadSplitButtonHandle, type UploadItem as PickItem,
} from './UploadSplitButton'
import { uploadItemsFromDrop } from './uploadDrop'

interface UseFilesUploadInput {
  selectedRoot: SyncRoot | null
  currentPath:  string
  mountMode:    boolean
  t: (key: string) => string
  /** Refetch the listing after the queue drains. */
  loadFiles: (rootID: string, path: string) => void | Promise<void>
}

/**
 * Owns the upload pipeline: queue display state, drag-over flag, the
 * UploadSplitButton ref, and the actual per-file PUT loop. Returns the
 * pieces FilesPage needs to wire into JSX (button, dropzone, queue
 * widget) plus the queue setter so the empty-queue clear button works.
 */
export function useFilesUpload(input: UseFilesUploadInput) {
  const { selectedRoot, currentPath, mountMode, t, loadFiles } = input

  const uploadBtnRef = useRef<UploadSplitButtonHandle>(null)
  const [uploadQueue, setUploadQueue] = useState<UploadItem[]>([])
  const [dragOver, setDragOver] = useState(false)

  const uploadFiles = useCallback(async (items: PickItem[]) => {
    if (!selectedRoot || items.length === 0) return

    setUploadQueue((prev) => [
      ...prev,
      ...items.map((it) => ({ name: it.relPath, progress: 0, done: false })),
    ])

    for (const { file, relPath } of items) {
      const filePath = currentPath.endsWith('/')
        ? currentPath + relPath
        : currentPath + '/' + relPath

      setUploadQueue((prev) =>
        prev.map((item) => item.name === relPath ? { ...item, progress: 10 } : item)
      )

      try {
        if (mountMode) {
          await apiClient.sharedMounts.upload(selectedRoot.id, filePath, file, new Date(file.lastModified))
        } else {
          await apiClient.files.upload(selectedRoot.id, filePath, file, new Date(file.lastModified))
        }
        setUploadQueue((prev) =>
          prev.map((item) => item.name === relPath ? { ...item, progress: 100, done: true } : item)
        )
      } catch (err: unknown) {
        const msg = (err as { body?: { error?: string } } | null)?.body?.error ?? t('files.uploadError')
        setUploadQueue((prev) =>
          prev.map((item) =>
            item.name === relPath ? { ...item, error: msg } : item
          )
        )
      }
    }

    await loadFiles(selectedRoot.id, currentPath)

    setTimeout(() => {
      setUploadQueue((prev) => prev.filter((item) => !item.done && !item.error))
    }, 4000)
  }, [selectedRoot, currentPath, mountMode, loadFiles, t])

  const handleDrop = useCallback(async (e: React.DragEvent) => {
    e.preventDefault()
    setDragOver(false)
    const items = await uploadItemsFromDrop(e.dataTransfer)
    if (items.length > 0) await uploadFiles(items)
  }, [uploadFiles])

  return {
    uploadBtnRef,
    uploadQueue, setUploadQueue,
    dragOver, setDragOver,
    uploadFiles, handleDrop,
  }
}
