package store

// Драйвер SQLite без CGO. Подключается только здесь, чтобы остальной код
// зависел от database/sql, а не от конкретного драйвера.
import _ "modernc.org/sqlite"

// driverName это имя, под которым драйвер регистрируется в database/sql.
const driverName = "sqlite"
