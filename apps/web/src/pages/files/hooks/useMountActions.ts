import { useCallback, useState } from 'react'
import type { SharedMount } from '@privasync/api-client'
import { useTranslation } from '@privasync/i18n'
import { apiClient } from '../../../store/auth'
import { startBrowserDownload } from '../../../lib/browserDownload'

// File/ZIP downloads + recipient-side "leave mount" dialog state for the
// shared-mounts row in the files page. setError is shared with the page so
// the global error banner picks up failures. ADR-0010 / ADR-0012.
export function useMountActions(
  setError: (msg: string | null) => void,
  onMountLeft: (mountID: string) => void,
) {
  const { t } = useTranslation()

  // Both go through the one-time token URL; the server serves a file mount
  // as the file and a directory mount as ZIP (ADR-0073).
  const downloadMountFile = useCallback(async (mount: SharedMount) => {
    try {
      startBrowserDownload(await apiClient.sharedMounts.downloadUrl(mount.id), mount.name)
    } catch (err) {
      console.error('download mount file failed', err)
      setError(t('files.downloadFailed'))
    }
  }, [setError, t])

  const downloadMountZip = useCallback(async (mount: SharedMount) => {
    try {
      startBrowserDownload(await apiClient.sharedMounts.downloadUrl(mount.id), mount.name + '.zip')
    } catch {
      setError(t('files.downloadFailed'))
    }
  }, [setError, t])

  const [mountLeaveTarget, setMountLeaveTarget] = useState<SharedMount | null>(null)
  const [mountLeaveError, setMountLeaveError] = useState<string | null>(null)
  const [mountLeaveLoading, setMountLeaveLoading] = useState(false)

  const handleLeaveMount = useCallback(async () => {
    if (!mountLeaveTarget) return
    setMountLeaveLoading(true)
    setMountLeaveError(null)
    try {
      await apiClient.sharedMounts.leave(mountLeaveTarget.id)
      onMountLeft(mountLeaveTarget.id)
      setMountLeaveTarget(null)
    } catch (err: any) {
      setMountLeaveError(err?.body?.error ?? t('files.deleteRootFailed'))
    } finally {
      setMountLeaveLoading(false)
    }
  }, [mountLeaveTarget, onMountLeft, t])

  return {
    downloadMountFile,
    downloadMountZip,
    mountLeaveTarget,
    setMountLeaveTarget,
    mountLeaveError,
    setMountLeaveError,
    mountLeaveLoading,
    handleLeaveMount,
  }
}
