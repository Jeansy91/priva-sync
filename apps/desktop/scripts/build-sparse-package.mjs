import { spawnSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const appDir = join(dirname(fileURLToPath(import.meta.url)), '..')
const onWindows = process.platform === 'win32'

const platform = (process.env.TAURI_ENV_PLATFORM ?? '').toLowerCase()
const target = (
  process.env.TAURI_ENV_TARGET_TRIPLE ??
  process.env.TARGET ??
  ''
).toLowerCase()
const windowsTarget =
  platform === 'windows' ||
  target.includes('windows') ||
  (!platform && !target && onWindows)

if (!windowsTarget) {
  console.log('[sparse-package] skipped for non-Windows target')
  process.exit(0)
}

const python = onWindows ? 'python' : 'python3'
const result = spawnSync(python, ['scripts/build_sparse_package.py'], {
  cwd: appDir,
  shell: onWindows,
  stdio: 'inherit',
})

if (result.error) {
  console.error(`[sparse-package] Python unavailable: ${result.error.message}`)
  process.exit(1)
}
process.exit(result.status ?? 1)
