// Заглушка сборки фронтенда: пишет минимальный dist/index.html.
// Dev 8 удаляет этот файл вместе с переходом на Vite.
import { mkdirSync, writeFileSync } from 'node:fs'

mkdirSync('dist', { recursive: true })
writeFileSync(
  'dist/index.html',
  `<!doctype html>
<html lang="ru">
  <head>
    <meta charset="utf-8" />
    <meta http-equiv="Content-Security-Policy" content="default-src 'self'" />
    <title>Wallet</title>
  </head>
  <body>
    <p>Wallet: каркас приложения. Интерфейс появится позже.</p>
  </body>
</html>
`,
)
