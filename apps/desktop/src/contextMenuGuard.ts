let installed = false

export function installContextMenuGuard(): void {
  if (installed) return
  installed = true
  window.addEventListener('contextmenu', (e) => e.preventDefault())
}
