package store

import "strings"

// Драйвер отдаёт ошибки SQLite текстом, у которого есть стабильные имена
// ограничений («UNIQUE constraint failed», «FOREIGN KEY constraint failed»);
// проверяем по ним, чтобы не зависеть от типов конкретного драйвера.

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func isForeignKey(err error) bool {
	return err != nil && strings.Contains(err.Error(), "FOREIGN KEY constraint failed")
}
