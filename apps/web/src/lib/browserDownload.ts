/**
 * Hands `url` to the browser's own download manager. Used with the one-time
 * /download/{token} URLs: the browser streams the body straight to disk and
 * shows progress immediately, instead of the page buffering the whole file
 * as a Blob first — which for a multi-GB file meant minutes with no visible
 * reaction and the full size in RAM (ADR-0073).
 */
export function startBrowserDownload(url: string, filename: string): void {
  const a = document.createElement('a')
  a.href     = url
  a.download = filename
  a.rel      = 'noopener'
  document.body.appendChild(a)
  a.click()
  a.remove()
}
