package logging

import "fmt"

// sprint печатает значение так же, как это сделал бы обработчик slog для
// произвольного типа. secret.Bytes при этом печатает только "[REDACTED]".
func sprint(v any) string {
	if e, ok := v.(error); ok {
		return e.Error()
	}
	return fmt.Sprintf("%+v", v)
}
