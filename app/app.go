// Package app это фасад Wails: методы, которые видит интерфейс.
//
// Зона Dev 7. Сейчас здесь заглушка из PR #0, чтобы main.go собирался.
package app

// App это приложение. Методы Wails появятся у Dev 7.
type App struct{}

// New создаёт пустое приложение.
func New() *App { return &App{} }
