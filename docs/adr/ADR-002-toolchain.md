# ADR-002: Инструменты сборки

14 ноября 2026 · Dev 1 · статус: принято, версии подтверждены `wails doctor`

## Решение

| Инструмент | Версия | Источник |
| --- | --- | --- |
| Go | 1.27.3 (`go 1.27.3` в `go.mod`) | `wails doctor` на Windows 10 Pro 22H2; решение владельца 15 ноября 2026 |
| Wails | v2.17.0 | `wails doctor`; последний стабильный v2, v3 остаётся бета-версией (решение D1) |
| Node.js | 24.13.1 (npm 11.8.0) | `wails doctor` |
| WebView2 | 157.0.4341.46 | `wails doctor` |

Переход на Wails v3 только отдельным ADR.

## Что подтверждено

`wails doctor` на Windows 10 Pro (build 19045), amd64: «Your system is ready for Wails development». `go.sum` создан, зависимость Wails v2.17.0 зафиксирована в `go.mod`. `wails build` на Windows (go1.27.3, Node 24.13.1) собрал `build/bin/wallet.exe` за 14,8 с, 15 ноября 2026.

## Открыто

- **Node.** В окружении Dev 1 проверялась 22.x, на машине сборки 24.13.1. Закрепить в CI одну мажорную версию, когда появится настоящий `package.json` у Dev 8.
- **Заглушка фронтенда** (`frontend/package.json`, `build-placeholder.mjs`) временная, лежит в зоне Dev 8, он заменяет её на Vue 3 + Vite.
