import { Button } from '@privasync/ui'
import { useTranslation } from '@privasync/i18n'
import { AlertCircle, Download } from 'lucide-react'

/**
 * Writes the user's TOTP backup codes to a plain-text file with a small
 * header so the user has context after downloading. Each code on its
 * own line, dashes preserved — same format the API hands them out in.
 */
export function downloadBackupCodes(
  codes: string[],
  username: string,
  t: (k: string) => string,
) {
  const header = [
    t('settings.totp.fileHeader'),
    `${t('settings.totp.fileAccount')}: ${username}`,
    `${t('settings.totp.fileCreated')}: ${new Date().toISOString()}`,
    '',
    t('settings.totp.fileNote1'),
    t('settings.totp.fileNote2'),
    '',
    '─────────────────────────',
    '',
  ].join('\n')
  const body = codes.join('\n') + '\n'
  const blob = new Blob([header + body], { type: 'text/plain;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `privasync-backup-codes-${username}.txt`
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
}

/**
 * One-shot display of freshly minted TOTP backup codes. Shown exactly
 * once — once the user clicks "Done", the codes are never available
 * again (the server only keeps hashes).
 */
export function BackupCodesDisplay({
  codes,
  username,
  onDone,
}: {
  codes: string[]
  username: string
  onDone: () => void
}) {
  const { t } = useTranslation()
  return (
    <div className="rounded-md border border-amber-500/40 bg-amber-50/30 dark:bg-amber-950/20 p-4 space-y-3">
      <div className="flex items-start gap-2">
        <AlertCircle className="h-4 w-4 mt-0.5 text-amber-600 shrink-0" />
        <div className="text-sm">
          <p className="font-medium text-amber-700 dark:text-amber-400">
            {t('settings.totp.saveCodesNow')}
          </p>
          <p className="text-muted-foreground mt-1">
            {t('settings.totp.saveCodesHint')}
          </p>
        </div>
      </div>
      <div className="grid grid-cols-2 gap-2 font-mono text-sm">
        {codes.map((code, i) => (
          <div
            key={i}
            className="bg-muted rounded-sm px-3 py-1.5 text-center tracking-widest select-all"
          >
            {code}
          </div>
        ))}
      </div>
      <div className="flex gap-2">
        <Button
          variant="outline"
          size="sm"
          onClick={() => downloadBackupCodes(codes, username, t)}
        >
          <Download className="h-3.5 w-3.5" />
          {t('settings.totp.downloadCodes')}
        </Button>
        <Button size="sm" onClick={onDone}>
          {t('settings.totp.codesDone')}
        </Button>
      </div>
    </div>
  )
}
