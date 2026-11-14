package config

import (
	"fmt"
	"log/slog"
	"sort"
)

// APIKeys это ключи API провайдеров, пришедшие из настроек бэкенда. Они не
// секрет кошелька, но их утечка стоит денег и лимитов, поэтому тип не
// печатает значения: ни fmt, ни slog, ни JSON.
//
// Нулевое значение пригодно к работе и не содержит ключей.
type APIKeys struct {
	m map[Provider]string
}

// NewAPIKeys копирует ключи, пустые значения пропускает.
func NewAPIKeys(keys map[Provider]string) APIKeys {
	out := make(map[Provider]string, len(keys))
	for p, k := range keys {
		if k != "" {
			out[p] = k
		}
	}
	return APIKeys{m: out}
}

// Get возвращает ключ провайдера. Второе значение false, если ключа нет.
func (k APIKeys) Get(p Provider) (string, bool) {
	v, ok := k.m[p]
	return v, ok
}

// Values возвращает все ключи для регистрации в logging.Redactor.
func (k APIKeys) Values() []string {
	out := make([]string, 0, len(k.m))
	for _, v := range k.m {
		out = append(out, v)
	}
	return out
}

// Providers возвращает провайдеров, для которых ключ задан, по алфавиту.
func (k APIKeys) Providers() []Provider {
	out := make([]Provider, 0, len(k.m))
	for p := range k.m {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Format печатает только имена провайдеров с ключами.
func (k APIKeys) Format(f fmt.State, _ rune) {
	_, _ = fmt.Fprintf(f, "APIKeys%v", k.Providers())
}

// LogValue пишет в лог только имена провайдеров.
func (k APIKeys) LogValue() slog.Value {
	return slog.StringValue(fmt.Sprint(k))
}

// MarshalJSON запрещает сериализацию ключей.
func (APIKeys) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("config: API keys must never be serialized")
}
