// Baut die COM-Shell-Extension (privasync_shell.dll) fuer Windows.
//
// Immer mit explizitem --target, damit der Ausgabepfad unabhaengig davon ist,
// wie der Tauri-Build aufgerufen wurde. Vorher lag die DLL unter
// shell/target/release/ — dieser Pfad stimmt nur ohne --target, weshalb jeder
// Aufruf mit Target-Angabe (Cross-Build, CI-Matrix) am fehlenden Resource-File
// scheiterte, und zwar erst spaet im Build-Script von tauri-build.
//
// Der Unterschied zwischen den Hosts ist das Werkzeug, nicht das Ziel:
// Windows hat die MSVC-Toolchain lokal, auf Linux liefert cargo-xwin die
// CRT- und SDK-Header nach.
//
// Auf Nicht-Windows ohne cargo-xwin wird uebersprungen: dieser Hook laeuft bei
// jedem Tauri-Build, auch beim Linux-Bundle, das die DLL nicht einbindet
// (sie steht in tauri.windows.conf.json, die nur fuer Windows-Ziele gilt).
// Ein Windows-Build ohne DLL faellt trotzdem auf — tauri-build bricht dann mit
// "resource path ... doesn't exist" ab.
import { spawnSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const TARGET = 'x86_64-pc-windows-msvc'
const shellDir = join(dirname(fileURLToPath(import.meta.url)), '..', 'shell')
const onWindows = process.platform === 'win32'

function run(args, opts = {}) {
  return spawnSync('cargo', args, { cwd: shellDir, shell: onWindows, ...opts })
}

if (!onWindows && run(['xwin', '--version'], { stdio: 'ignore' }).status !== 0) {
  console.log('[build-shell] cargo-xwin nicht vorhanden — uebersprungen (kein Windows-Ziel).')
  process.exit(0)
}

const args = onWindows
  ? ['build', '--release', '--target', TARGET]
  : ['xwin', 'build', '--release', '--target', TARGET]

console.log(`[build-shell] cargo ${args.join(' ')} (cwd: ${shellDir})`)
const res = run(args, { stdio: 'inherit' })

if (res.error) {
  console.error(`[build-shell] cargo nicht ausfuehrbar: ${res.error.message}`)
  process.exit(1)
}
process.exit(res.status ?? 1)
