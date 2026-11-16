# Wallet

Desktop self-custody wallet built with **Wails v2, Go 1.27.4, Vue 3 and TypeScript**.

## Development

Requirements:

- Go 1.27.4 (the version pinned by `go.mod`)
- Node.js 24 and npm
- Wails CLI 2.17.0
- Windows 10/11 with WebView2 for the desktop build

Install frontend dependencies and run the standalone UI:

```powershell
cd frontend
npm ci
npm run dev
```

The standalone Vite UI uses the explicit mock adapter in development. To run the actual Go backend through Wails, set `VITE_WALLET_API=wails` before starting Wails:

```powershell
$env:VITE_WALLET_API = "wails"
wails dev
```

Build the Windows desktop application:

```powershell
wails build -clean -platform windows/amd64
```

## Provider configuration

Provider credentials are read by the Go composition root from environment variables and are never sent to the Vue layer:

| Variable | Purpose |
|---|---|
| `ALCHEMY_API_KEY` | Preferred Ethereum JSON-RPC endpoint; PublicNode is used as fallback |
| `ETHERSCAN_API_KEY` | Ethereum transaction/history indexer |
| `TRONGRID_API_KEY` | TRON / Nile API request header |

Bitcoin uses the configured public Esplora endpoints. Do not commit API keys or place them in frontend `.env` files.

## Data and backups

Application data is stored below `os.UserConfigDir()/wallet/`, separated by network profile:

- `test/` — Bitcoin testnet, Ethereum Sepolia and TRON Nile
- `main/` — Bitcoin mainnet, Ethereum mainnet and TRON mainnet

Encrypted keystore files and the SQLite database remain local. Backups are written below the active profile's `backups/` directory. Keep the BIP-39 recovery phrase offline; a backup archive does not replace it.

## Validation

The CI workflow checks frontend install, typecheck, lint, unit tests and production build; Go build, vet, race-enabled tests and vulnerability scanning on Ubuntu and Windows; and the Windows Wails executable build.

Exchange/swap functionality is intentionally outside the initial wallet scope.
