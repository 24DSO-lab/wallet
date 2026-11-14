// Package secret хранит секреты (seed, ключи, пароль, парольную фразу) так,
// чтобы их нельзя было вывести случайно.
//
// Правила ADR для секретов:
//   - отдельный тип без строкового представления, данные это срез байтов;
//   - после использования буфер затирается (Wipe);
//   - секрет не попадает в логи, ошибки, JSON и DTO.
//
// Пакет создал Dev 1, дальше им владеет Dev 2.
package secret

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
)

// redacted это единственный текст, который тип печатает о себе.
const redacted = "[REDACTED]"

// ErrNotSerializable возвращается при попытке закодировать секрет в JSON или текст.
var ErrNotSerializable = errors.New("secret: value must never be serialized")

// buffer отделён от Bytes намеренно: Bytes хранит только указатель. Если Bytes
// окажется неэкспортируемым полем структуры, которую напечатают через %v,
// fmt выведет лишь адрес указателя, а не содержимое среза.
type buffer struct {
	data []byte
}

// Bytes это секретная последовательность байтов.
//
// Нулевое значение пригодно к работе и считается пустым секретом. Копия Bytes
// указывает на тот же буфер, поэтому Wipe на любой копии затирает общие данные:
// у секрета один владелец, который отвечает за затирание.
//
// Тип не имеет метода String и не кодируется в JSON или текст (кодирование
// возвращает ErrNotSerializable), а любая печать через fmt даёт "[REDACTED]".
type Bytes struct {
	p *buffer
}

// FromBytes оборачивает b без копирования и забирает владение: вызывающий не
// должен читать, менять или хранить b после вызова. Для источника, который
// нужно оставить нетронутым, используйте Copy.
func FromBytes(b []byte) Bytes {
	if b == nil {
		return Bytes{}
	}
	return Bytes{p: &buffer{data: b}}
}

// Copy копирует b в новый буфер. Исходный срез остаётся за вызывающим, и
// затирать его тоже должен он.
func Copy(b []byte) Bytes {
	if b == nil {
		return Bytes{}
	}
	return Bytes{p: &buffer{data: append([]byte(nil), b...)}}
}

// FromString копирует s в новый буфер. Сама строка неизменяема, затереть её
// нельзя, поэтому по возможности не создавайте секретные строки вообще: на
// границе Wails пароль приходит строкой, и её надо переносить сюда сразу.
func FromString(s string) Bytes {
	if s == "" {
		return Bytes{}
	}
	return Bytes{p: &buffer{data: []byte(s)}}
}

// Len возвращает длину секрета в байтах.
func (b Bytes) Len() int {
	if b.p == nil {
		return 0
	}
	return len(b.p.data)
}

// IsEmpty сообщает, что секрет пуст или уже затёрт и освобождён.
func (b Bytes) IsEmpty() bool {
	return b.Len() == 0
}

// Expose возвращает внутренний срез, а не копию. Используйте только чтобы
// передать данные криптографической функции. Не печатайте результат, не
// сохраняйте его и не превращайте в строку: любая копия останется в памяти
// после Wipe. После Wipe возвращает nil.
func (b Bytes) Expose() []byte {
	if b.p == nil {
		return nil
	}
	return b.p.data
}

// Clone создаёт независимый секрет с теми же данными. У клона свой владелец,
// который затирает его отдельно.
func (b Bytes) Clone() Bytes {
	if b.IsEmpty() {
		return Bytes{}
	}
	return Copy(b.p.data)
}

// Equal сравнивает два секрета за время, не зависящее от содержимого.
func (b Bytes) Equal(other Bytes) bool {
	return subtle.ConstantTimeCompare(b.Expose(), other.Expose()) == 1
}

// Wipe затирает данные нулями и освобождает буфер. Повторный вызов и вызов
// на нулевом значении безопасны. Затирание действует на все копии Bytes,
// указывающие на этот буфер. Сборщик мусора Go мог раньше скопировать данные
// в другое место: этот остаточный риск ADR принимает.
func (b Bytes) Wipe() {
	if b.p == nil {
		return
	}
	clear(b.p.data)
	runtime.KeepAlive(b.p.data)
	b.p.data = nil
}

// Format реализует fmt.Formatter и подавляет любую печать содержимого: %v,
// %+v, %#v, %s, %q, %x, %d и остальные глаголы дают один и тот же текст.
func (Bytes) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte(redacted))
}

// LogValue реализует slog.LogValuer: в лог попадает только пометка.
func (Bytes) LogValue() slog.Value {
	return slog.StringValue(redacted)
}

// MarshalJSON всегда возвращает ошибку: секрет не сериализуется.
func (Bytes) MarshalJSON() ([]byte, error) {
	return nil, ErrNotSerializable
}

// MarshalText всегда возвращает ошибку: секрет не сериализуется.
func (Bytes) MarshalText() ([]byte, error) {
	return nil, ErrNotSerializable
}

// UnmarshalJSON всегда возвращает ошибку: секрет не читается из JSON.
func (*Bytes) UnmarshalJSON([]byte) error {
	return ErrNotSerializable
}

// UnmarshalText всегда возвращает ошибку: секрет не читается из текста.
func (*Bytes) UnmarshalText([]byte) error {
	return ErrNotSerializable
}
