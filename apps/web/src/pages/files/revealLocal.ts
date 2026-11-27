// "Lokal anzeigen" via privasync:// protocol handler. The desktop
// client registers the scheme and shows the entry's directory in the OS file
// manager (folders open themselves, files their containing directory).

export function buildRevealUrl(rootId: string, path: string): string {
  return `privasync://reveal?root=${encodeURIComponent(rootId)}&path=${encodeURIComponent(path)}`
}

// Navigate the browser to the deep link without leaving the page: a click on
// a detached anchor hands the URL to the OS protocol handler, no new tab.
export function revealLocally(rootId: string, path: string): void {
  const a = document.createElement('a')
  a.href = buildRevealUrl(rootId, path)
  a.click()
}
