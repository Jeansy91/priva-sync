import { useState, useEffect, useCallback } from 'react'
import type { AdminUser } from '@privasync/api-client'
import { apiClient } from '../../store/auth'

interface UseAdminUsersResult {
  users:        AdminUser[]
  setUsers:     React.Dispatch<React.SetStateAction<AdminUser[]>>
  loading:      boolean
  error:        string | null
  success:      string | null
  setError:     (msg: string | null) => void
  setSuccess:   (msg: string | null) => void
  togglingActive: string | null
  togglingRole:   string | null
  unlocking:      string | null
  recomputing:    string | null
  handleUnlock:       (u: AdminUser) => Promise<void>
  handleRecompute:    (u: AdminUser) => Promise<void>
  handleToggleRole:   (u: AdminUser) => Promise<void>
  handleToggleActive: (u: AdminUser) => Promise<void>
}

const SUCCESS_TTL_MS = 3000

/**
 * Owns the admin user inventory and the four per-row mutating actions
 * (unlock, recompute quota, toggle role, toggle active). Loading +
 * success flashes are baked in so the page can stay focused on layout.
 */
export function useAdminUsers(t: (k: string, vars?: Record<string, unknown>) => string): UseAdminUsersResult {
  const [users, setUsers] = useState<AdminUser[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [success, setSuccess] = useState<string | null>(null)
  const [togglingActive, setTogglingActive] = useState<string | null>(null)
  const [togglingRole, setTogglingRole]     = useState<string | null>(null)
  const [unlocking, setUnlocking]           = useState<string | null>(null)
  const [recomputing, setRecomputing]       = useState<string | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const list = await apiClient.admin.listUsers()
      setUsers(list ?? [])
    } catch {
      setError(t('admin.loadFailed'))
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => { load() }, [load])

  const flashSuccess = (msg: string) => {
    setSuccess(msg)
    setTimeout(() => setSuccess(null), SUCCESS_TTL_MS)
  }

  const apiErr = (err: unknown, fallback: string) =>
    (err as { body?: { error?: string } } | null)?.body?.error ?? fallback

  const handleUnlock = async (user: AdminUser) => {
    setUnlocking(user.id)
    setError(null)
    try {
      await apiClient.admin.unlockUser(user.id)
      setUsers((prev) =>
        prev.map((u) => u.id === user.id ? { ...u, is_locked: false } : u)
      )
      flashSuccess(t('admin.msgUnlocked', { user: user.username }))
    } catch (err: unknown) {
      setError(apiErr(err, t('admin.errorGeneric')))
    } finally {
      setUnlocking(null)
    }
  }

  const handleRecompute = async (user: AdminUser) => {
    setRecomputing(user.id)
    setError(null)
    try {
      const r = await apiClient.admin.recomputeQuota(user.id)
      setUsers((prev) =>
        prev.map((u) => u.id === user.id
          ? { ...u, used_bytes: r.used_bytes, trash_bytes: r.trash_bytes }
          : u)
      )
      flashSuccess(t('admin.msgRecomputed', { user: user.username }))
    } catch (err: unknown) {
      setError(apiErr(err, t('admin.errorGeneric')))
    } finally {
      setRecomputing(null)
    }
  }

  const handleToggleRole = async (user: AdminUser) => {
    const next = user.role === 'admin' ? 'user' : 'admin'
    setTogglingRole(user.id)
    setError(null)
    try {
      await apiClient.admin.setRole(user.id, next)
      setUsers((prev) => prev.map((u) => u.id === user.id ? { ...u, role: next } : u))
      flashSuccess(
        next === 'admin'
          ? t('admin.msgPromoted', { user: user.username })
          : t('admin.msgDemoted', { user: user.username }),
      )
    } catch (err: unknown) {
      setError(apiErr(err, t('admin.errorGeneric')))
    } finally {
      setTogglingRole(null)
    }
  }

  const handleToggleActive = async (user: AdminUser) => {
    setTogglingActive(user.id)
    setError(null)
    try {
      await apiClient.admin.setActive(user.id, !user.is_active)
      setUsers((prev) =>
        prev.map((u) => u.id === user.id ? { ...u, is_active: !u.is_active } : u)
      )
      flashSuccess(
        !user.is_active
          ? t('admin.msgUnlockedActive', { user: user.username })
          : t('admin.msgLocked', { user: user.username }),
      )
    } catch (err: unknown) {
      setError(apiErr(err, t('admin.errorGeneric')))
    } finally {
      setTogglingActive(null)
    }
  }

  return {
    users, setUsers, loading, error, success,
    setError, setSuccess,
    togglingActive, togglingRole, unlocking, recomputing,
    handleUnlock, handleRecompute, handleToggleRole, handleToggleActive,
  }
}
