import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'

const wailsDevCsp = {
  name: 'wails-dev-csp',
  transformIndexHtml(html: string) {
    return html.replace(
      "connect-src 'self' ipc: http://wails.localhost https://wails.localhost",
      "connect-src 'self' ipc: http://wails.localhost https://wails.localhost ws://127.0.0.1:5173",
    )
  },
}

export default defineConfig(({ mode }) => ({
  plugins: [vue(), ...(mode === 'wails' ? [wailsDevCsp] : [])],
  base: './',
  server: { host: '127.0.0.1' },
  test: { environment: 'jsdom', globals: true, include: ['src/**/*.spec.ts'] },
}))
