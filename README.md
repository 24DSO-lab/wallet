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

The standalone Vite UI uses the mock adapter for UI work. Wails development automatically loads `frontend/.env.wails` and binds the real Go API:

```powershell
node scripts/wails-dev.mjs
```

The launcher adds only `wails.localhost` to Wails' development IPC host allowlist. This is required by Wails 2.17+'s dev-server host validation; it does not disable the validation or change the production CSP.

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

## Frontend structure

```
frontend/src/
  api/         Api contract, in-memory mock and the Wails binding
  components/  shell, asset tiles and the Settings cards
  i18n/        ru and en dictionaries (en is typed against ru)
  lib/         amount, validation, error-code and clipboard helpers
  stores/      Pinia: session, wallets, send flow, import check, UI feedback
  views/       one component per route
  router.ts    hash routes and the session guard
```

The router only lets a locked session reach `/lock`. Passwords, BIP-39 passphrases and recovery phrases stay in the component that collects them and are cleared when it unmounts; Pinia stores never hold them. Go reports failures as stable error codes, which `lib/errors.ts` maps to translated messages. Every user-facing string lives in `i18n/`; add a key to both dictionaries.

Bitcoin uses the configured public Esplora endpoints. Do not commit API keys or place them in frontend `.env` files.

## Data and backups

Application data is stored below `os.UserConfigDir()/wallet/`, separated by network profile:

- `test/` — Bitcoin testnet, Ethereum Sepolia and TRON Nile
- `main/` — Bitcoin mainnet, Ethereum mainnet and TRON mainnet

Encrypted keystore files and the SQLite database remain local. Backups are written to a folder selected in the native directory picker and can be restored from Settings. New wallet creation persists the mnemonic only after Go verifies the recovery-word challenge. Settings also supports explicit seed reveal, password change and confirmed wallet deletion. Keep the BIP-39 recovery phrase offline; a backup archive does not replace it.

## Validation

The CI workflow checks frontend install, typecheck, lint, unit tests and production build; Go build, vet, race-enabled tests and vulnerability scanning on Ubuntu and Windows; and the Windows Wails executable build.

Exchange/swap functionality is intentionally outside the initial wallet scope.

## Приёмка перед использованием

Автоматические проверки сборки и тестов запускаются в GitHub Actions. Они не
заменяют реальные тестовые отправки и сверку истории с обозревателями. Статус
гейтов ADR-001 и необходимые доказательства см. в [docs/gates](docs/gates/README.md).
До закрытия гейтов 1, 2, 3 и 5 не используйте приложение для реальных сумм.
Этап обмена отложен отдельным решением и не входит в v1.
