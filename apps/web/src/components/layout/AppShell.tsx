import { type ReactNode, useEffect, useState } from 'react'
import { Link, useLocation, useNavigate } from 'react-router-dom'
import { Files, Share2, AlertTriangle, Settings, Users, LogOut, Menu, X, HardDrive, Trash2, FileClock, ChevronRight, BarChart3, Wrench, Monitor, Download } from 'lucide-react'
import { Button, cn } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import { apiClient, useAuthStore } from '../../store/auth'
import { useSSE } from '../../hooks/useSSE'
import { typed, type Me } from '@privasync/api-client'
import { AudioPlayer } from '../../pages/files/AudioPlayer'

interface NavItem {
  href: string
  labelKey: string
  icon: ReactNode
  adminOnly?: boolean
  matchPrefix?: boolean
  sectionKey?: string
}

const NAV_ITEMS: NavItem[] = [
  { href: '/files',           labelKey: 'appShell.files',     icon: <Files className="h-4 w-4" />, matchPrefix: true },
  { href: '/shares',          labelKey: 'appShell.shares',    icon: <Share2 className="h-4 w-4" /> },
  { href: '/conflicts',       labelKey: 'appShell.conflicts', icon: <AlertTriangle className="h-4 w-4" /> },
  { href: '/trash',           labelKey: 'appShell.trash',     icon: <Trash2 className="h-4 w-4" /> },
  { href: '/settings',        labelKey: 'appShell.settings',  icon: <Settings className="h-4 w-4" /> },
  { href: '/admin/users',     labelKey: 'admin.nav.users',    icon: <Users className="h-4 w-4" />,     adminOnly: true, sectionKey: 'admin.nav.section' },
  { href: '/admin/stats',     labelKey: 'admin.nav.stats',    icon: <BarChart3 className="h-4 w-4" />, adminOnly: true, sectionKey: 'admin.nav.section' },
  { href: '/admin/jobs',      labelKey: 'admin.nav.jobs',     icon: <Wrench className="h-4 w-4" />,    adminOnly: true, sectionKey: 'admin.nav.section' },
  { href: '/admin/clients',   labelKey: 'admin.nav.clients',  icon: <Monitor className="h-4 w-4" />,   adminOnly: true, sectionKey: 'admin.nav.section' },
  { href: '/admin/audit-log', labelKey: 'admin.nav.auditLog', icon: <FileClock className="h-4 w-4" />, adminOnly: true, sectionKey: 'admin.nav.section' },
]

interface SidebarProps {
  className?: string
  navItems: NavItem[]
  usedPercent: number | null
  trashBytes: number
  user: Me | null
  pathname: string
  onCloseMobile: () => void
  onLogout: () => void
}

interface AvatarUser {
  id: string
  username: string
  avatar_url: string | null
}

interface UserAvatarProps {
  user: AvatarUser | null
  size: number
}

export function UserAvatar({ user, size }: UserAvatarProps) {
  const initials = (user?.username ?? '?').slice(0, 2).toUpperCase()
  const dim = { width: size, height: size }
  const [blobUrl, setBlobUrl] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    let url: string | null = null
    if (user?.avatar_url && user.id) {
      apiClient.auth.fetchAvatar(user.id).then((blob) => {
        if (cancelled || !blob) return
        url = URL.createObjectURL(blob)
        setBlobUrl(url)
      }).catch(() => {})
    } else {
      setBlobUrl(null)
    }
    return () => {
      cancelled = true
      if (url) URL.revokeObjectURL(url)
    }
  }, [user?.avatar_url, user?.id])

  if (blobUrl) {
    return (
      <img
        src={blobUrl}
        alt={user?.username ?? ''}
        className="rounded-full object-cover bg-muted shrink-0"
        style={dim}
      />
    )
  }
  return (
    <div
      className="rounded-full bg-muted flex items-center justify-center text-xs font-medium text-muted-foreground shrink-0"
      style={dim}
      aria-label={user?.username ?? 'User'}
    >
      {initials}
    </div>
  )
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(1))} ${sizes[i]}`
}

function NavLink({ item, pathname, onCloseMobile, label }: {
  item: NavItem
  pathname: string
  onCloseMobile: () => void
  label: string
}) {
  return (
    <Link
      to={item.href}
      onClick={onCloseMobile}
      className={cn(
        'flex items-center gap-2.5 rounded-md px-3 py-2 text-sm font-medium transition-colors',
        (item.matchPrefix ? pathname.startsWith(item.href) : pathname === item.href)
          ? 'bg-accent text-accent-foreground'
          : 'text-muted-foreground hover:bg-accent hover:text-accent-foreground',
      )}
    >
      {item.icon}
      {label}
    </Link>
  )
}

function CollapsibleSection({
  sectionKey, items, pathname, onCloseMobile, t,
}: {
  sectionKey: string
  items: NavItem[]
  pathname: string
  onCloseMobile: () => void
  t: (k: string) => string
}) {
  const storageKey = `appShell.section.${sectionKey}`
  const containsActive = items.some((i) => pathname.startsWith(i.href))
  const [open, setOpen] = useState(() => {
    if (typeof window === 'undefined') return containsActive
    const v = window.localStorage.getItem(storageKey)
    if (v === '1') return true
    if (v === '0') return false
    return containsActive
  })
  useEffect(() => {
    if (containsActive) setOpen(true)
  }, [containsActive])
  const toggle = () => {
    setOpen((prev) => {
      const next = !prev
      try { window.localStorage.setItem(storageKey, next ? '1' : '0') } catch {}
      return next
    })
  }
  return (
    <div className="pt-3">
      <button
        type="button"
        onClick={toggle}
        aria-expanded={open}
        className="flex w-full items-center gap-1 px-3 pb-1 text-[10px] font-medium uppercase tracking-wide text-muted-foreground/70 hover:text-muted-foreground"
      >
        <ChevronRight className={cn('h-3 w-3 transition-transform', open && 'rotate-90')} />
        {t(sectionKey)}
      </button>
      {open && (
        <div className="space-y-0.5">
          {items.map((item) => (
            <NavLink
              key={item.href}
              item={item}
              pathname={pathname}
              onCloseMobile={onCloseMobile}
              label={t(item.labelKey)}
            />
          ))}
        </div>
      )}
    </div>
  )
}

function renderNav(
  navItems: NavItem[],
  pathname: string,
  onCloseMobile: () => void,
  t: (k: string) => string,
) {
  const out: ReactNode[] = []
  let i = 0
  while (i < navItems.length) {
    const item = navItems[i]
    if (!item.sectionKey) {
      out.push(
        <NavLink
          key={item.href}
          item={item}
          pathname={pathname}
          onCloseMobile={onCloseMobile}
          label={t(item.labelKey)}
        />,
      )
      i++
      continue
    }
    const sectionKey = item.sectionKey
    const group: NavItem[] = []
    while (i < navItems.length && navItems[i].sectionKey === sectionKey) {
      group.push(navItems[i])
      i++
    }
    // Collect following items belonging to the same section that lack their
    // own sectionKey marker, until a new sectionKey arrives or a gap appears.
    while (i < navItems.length && !navItems[i].sectionKey) {
      // Items without sectionKey no longer belong to the section — stop.
      break
    }
    out.push(
      <CollapsibleSection
        key={`sec-${sectionKey}`}
        sectionKey={sectionKey}
        items={group}
        pathname={pathname}
        onCloseMobile={onCloseMobile}
        t={t}
      />,
    )
  }
  return out
}

function Sidebar({ className, navItems, usedPercent, trashBytes, user, pathname, onCloseMobile, onLogout }: SidebarProps) {
  const { t } = useTranslation()
  return (
    <aside className={cn(
      'flex flex-col w-56 h-full overflow-y-auto border-r border-border bg-card px-3 py-4 gap-1',
      className
    )}>
      {/* Logo */}
      <div className="flex items-center gap-2 px-2 pb-4 border-b border-border mb-2">
        <img src="/logo.png" alt="" className="h-7 w-7" />
        <div className="flex-1 leading-tight">
          <span className="font-semibold text-sm block">PrivaSync</span>
          <span className="text-[10px] text-muted-foreground block">{t('appShell.version', { version: __APP_VERSION__ })}</span>
        </div>
        <Button
          variant="ghost"
          size="icon"
          className="md:hidden h-7 w-7"
          onClick={onCloseMobile}
          title={t('common.close')}
        >
          <X className="h-4 w-4" />
        </Button>
      </div>

      {/* Navigation */}
      <nav className="flex-1 space-y-0.5">
        {renderNav(navItems, pathname, onCloseMobile, t)}
      </nav>

      {/* Quota + Client-Download — Download bewusst kein eigener Nav-Punkt, sitzt dezent im Speicher-Block */}
      <div className="px-2 pb-1 space-y-1">
        {usedPercent !== null && (
          <>
            <div className="flex items-center justify-between text-xs text-muted-foreground">
              <span className="flex items-center gap-1"><HardDrive className="h-3 w-3" /> {t('appShell.storage')}</span>
              <span>{usedPercent}%</span>
            </div>
            <div className="h-1.5 rounded-full bg-secondary overflow-hidden">
              <div
                className={cn('h-full rounded-full transition-all', usedPercent >= 90 ? 'bg-destructive' : 'bg-primary')}
                style={{ width: `${usedPercent}%` }}
              />
            </div>
            {trashBytes > 0 && (
              <div className="flex items-center justify-between text-[10px] text-muted-foreground/80 pt-0.5">
                <span className="flex items-center gap-1"><Trash2 className="h-2.5 w-2.5" /> {t('appShell.trashLabel')}</span>
                <span>{formatBytes(trashBytes)}</span>
              </div>
            )}
          </>
        )}
        <Link
          to="/download"
          onClick={onCloseMobile}
          className="flex items-center justify-center gap-1 pt-2 text-[10px] text-muted-foreground/80 hover:text-muted-foreground"
        >
          <Download className="h-2.5 w-2.5" /> {t('appShell.download')}
        </Link>
      </div>

      {/* User / Logout */}
      <div className="border-t border-border pt-2 mt-1">
        <div className="flex items-center gap-2 px-2 py-1">
          <UserAvatar user={user} size={28} />
          <div className="min-w-0 flex-1">
            <p className="text-sm font-medium truncate">{user?.username}</p>
            <p className="text-xs text-muted-foreground capitalize">{user?.role}</p>
          </div>
          <Button variant="ghost" size="icon" onClick={onLogout} title={t('auth.logout')}>
            <LogOut className="h-4 w-4" />
          </Button>
        </div>
      </div>
    </aside>
  )
}

interface AppShellProps {
  children: ReactNode
}

export function AppShell({ children }: AppShellProps) {
  const { t } = useTranslation()
  const { user, setUser, logout } = useAuthStore()
  const navigate = useNavigate()
  const location = useLocation()
  const [sidebarOpen, setSidebarOpen] = useState(false)

  const handleLogout = async () => {
    try { await apiClient.auth.logout() } catch {}
    logout()
    navigate('/login')
  }

  // Sidebar-Quota-Bar muss auf Server-Mutationen reagieren — Upload/Delete
  // in einem anderen Tab oder vom Desktop-Client darf den Counter nicht stehen
  // lassen. Server feuert quota_updated nach jeder Quota-Mutation; wir holen
  // /me neu und aktualisieren den Store.
  useSSE({
    quota_updated: typed<'quota_updated'>(async () => {
      try { setUser(await apiClient.auth.me()) } catch { /* non-critical */ }
    }),
  })

  const navItems = NAV_ITEMS.filter((item) => !item.adminOnly || user?.role === 'admin')

  // Trash zaehlt seit der Vereinheitlichung mit zur Quota — der Bar zeigt
  // also (used + trash) / quota, nicht nur used.
  const usedPercent = user && user.quota_bytes > 0
    ? Math.min(100, Math.round(((user.used_bytes + user.trash_bytes) / user.quota_bytes) * 100))
    : null

  const trashBytes = user?.trash_bytes ?? 0

  const sidebarProps = {
    navItems,
    usedPercent,
    trashBytes,
    user,
    pathname: location.pathname,
    onCloseMobile: () => setSidebarOpen(false),
    onLogout: handleLogout,
  }

  return (
    <div className="flex h-dvh overflow-hidden">
      {/* Desktop Sidebar */}
      <Sidebar {...sidebarProps} className="hidden md:flex" />

      {/* Mobile Overlay */}
      {sidebarOpen && (
        <div className="fixed inset-0 z-40 md:hidden">
          <div className="absolute inset-0 bg-black/40" onClick={() => setSidebarOpen(false)} />
          <div className="relative z-50 flex h-full w-fit">
            <Sidebar {...sidebarProps} className="flex" />
          </div>
        </div>
      )}

      {/* Hauptinhalt */}
      <div className="flex-1 flex flex-col min-w-0 h-full">
        {/* Mobile Header */}
        <header className="md:hidden flex items-center gap-2 border-b border-border px-4 py-3 shrink-0">
          <Button variant="ghost" size="icon" onClick={() => setSidebarOpen(true)}>
            {sidebarOpen ? <X className="h-5 w-5" /> : <Menu className="h-5 w-5" />}
          </Button>
          <img src="/logo.png" alt="" className="h-6 w-6" />
          <div className="leading-tight">
            <span className="font-semibold text-sm block">PrivaSync</span>
            <span className="text-[10px] text-muted-foreground block">{t('appShell.version', { version: __APP_VERSION__ })}</span>
          </div>
        </header>

        <main className="flex-1 min-h-0">
          {children}
        </main>

        {/* Audio-Player Footer — rechts neben der Sidebar, überlebt Route-Wechsel */}
        <AudioPlayer />
      </div>
    </div>
  )
}
