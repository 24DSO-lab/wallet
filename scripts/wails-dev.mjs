import { spawn } from 'node:child_process'

const executable = process.platform === 'win32' ? 'wails.exe' : 'wails'
const env = { ...process.env }
const allowedHosts = new Set(
  (env.WAILS_DEV_ALLOWED_HOSTS ?? '')
    .split(',')
    .map((host) => host.trim())
    .filter(Boolean),
)

allowedHosts.add('wails.localhost')
env.WAILS_DEV_ALLOWED_HOSTS = [...allowedHosts].join(',')

const child = spawn(executable, ['dev', ...process.argv.slice(2)], {
  env,
  stdio: 'inherit',
})

child.on('error', (error) => {
  console.error(`Failed to start Wails dev server: ${error.message}`)
  process.exitCode = 1
})

child.on('exit', (code, signal) => {
  process.exitCode = code ?? (signal ? 1 : 0)
})
