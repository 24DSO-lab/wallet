import { mockApi } from './mock'
import { wailsApi } from './wails'

// Production bundles always bind to Go. The mock is opt-in for local UI work
// and tests, so a missing environment variable can never ship a fake wallet.
const useMock = import.meta.env.VITE_WALLET_API === 'mock' ||
  (import.meta.env.DEV && import.meta.env.VITE_WALLET_API !== 'wails')

export const api = useMock ? mockApi() : wailsApi()
