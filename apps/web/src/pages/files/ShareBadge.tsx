import { Link2, UserRound } from 'lucide-react'
import { Link } from 'react-router-dom'
import { useTranslation } from '@privasync/i18n'
import type { PathShareInfo } from '@privasync/api-client'

// One badge per share-type so the giver sees at a glance whether a path
// is shared with a concrete user, exposed via public link, or both.
// Multiple user-shares collapse into a single icon whose aria-label
// lists the recipients. When `href` is set the icons become navigation
// links so the giver can jump to the share-management page filtered
// to this path. See ADR-0010.
export function ShareBadge({ shares, href }: { shares: PathShareInfo[]; href?: string }) {
  const { t } = useTranslation()
  if (shares.length === 0) return null

  const userTargets = shares
    .filter((s) => s.share_type === 'user' && s.target_username)
    .map((s) => s.target_username as string)
  const hasPublic = shares.some((s) => s.share_type === 'public')

  const userLabel = t('files.badgeSharedWithUser', { username: userTargets.join(', ') })
  const publicLabel = t('files.badgeSharedPublicLink')

  const renderIcon = (label: string, icon: React.ReactNode) => {
    if (href) {
      return (
        <Link
          to={href}
          aria-label={label}
          onClick={(e) => e.stopPropagation()}
          className="inline-flex items-center"
        >
          {icon}
        </Link>
      )
    }
    return <span aria-label={label} className="inline-flex items-center">{icon}</span>
  }

  return (
    <span className="inline-flex items-center gap-1">
      {userTargets.length > 0 && renderIcon(
        userLabel,
        <UserRound className="h-3.5 w-3.5 text-blue-500" aria-hidden />,
      )}
      {hasPublic && renderIcon(
        publicLabel,
        <Link2 className="h-3.5 w-3.5 text-amber-500" aria-hidden />,
      )}
    </span>
  )
}
