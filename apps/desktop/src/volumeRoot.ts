/**
 * Mirror of `cloud_files::is_volume_root` (Rust) for the UI.
 *
 * The Cloud Filter API refuses to register a volume root such as `D:\`, so a
 * sync root there cannot carry placeholders and cannot be on-demand — it
 * syncs everything in full (ADR-0057). The backend enforces that; this copy
 * exists only so the UI can stop offering a choice it would then refuse.
 *
 * Keep the two in step: `add_sync_root` and `set_download_mode` are the
 * authority, this is the courtesy.
 */
export function isVolumeRoot(path: string): boolean {
  const p = path.trim()
  if (p === "") return false
  // Windows drive root: "D:\", "D:/", or bare "D:".
  if (/^[a-zA-Z]:[\\/]?$/.test(p)) return true
  // POSIX filesystem root.
  if (p === "/") return true
  // Bare UNC share: "\\server\share" with nothing below it.
  if (/^\\\\[^\\]+\\[^\\]+\\?$/.test(p)) return true
  return false
}
