package keystore

import (
	"time"

	"github.com/24DSO-lab/wallet/internal/keystore/argon2id"
)

// Params это параметры Argon2id, которые хранятся в заголовке файла.
type Params struct {
	Time      uint32 // число проходов
	MemoryKiB uint32 // память в КиБ
	Threads   uint8  // полосы
}

// Границы. Нижняя взята из рекомендаций OWASP для Argon2id: 19 МиБ, два
// прохода, одна полоса. Верхняя защищает от файла с заголовком, который
// требует много памяти и времени: заголовок разбирается до проверки пароля.
// При максимуме (256 МиБ, 6 проходов, 4 полосы) вычисление занимает секунды,
// но не зависает; подбор Calibrate всегда укладывается в эти границы.
const (
	minTime      = 2
	minMemoryKiB = 19 * 1024
	maxTime      = 6
	maxMemoryKiB = 256 * 1024
	maxThreads   = 4

	calibrateMaxKiB = 256 * 1024
	derivedKeyLen   = 32
)

// DefaultTarget это целевое время одного вычисления ключа (ориентир ADR и
// промпта: 0,5-1 секунда).
const DefaultTarget = 750 * time.Millisecond

func (p Params) withinReadBounds() bool {
	return p.Time >= 1 && p.Time <= maxTime &&
		p.Threads >= 1 && p.Threads <= maxThreads &&
		p.MemoryKiB >= 8*uint32(p.Threads) && p.MemoryKiB <= maxMemoryKiB
}

// acceptable сообщает, что параметры годятся для новых файлов: в границах
// чтения и не ниже минимума OWASP.
func (p Params) acceptable() bool { return p.withinReadBounds() && p.atLeastFloor() }

func (p Params) atLeastFloor() bool {
	return p.Time >= minTime && p.MemoryKiB >= minMemoryKiB && p.Threads >= 1
}

// Calibrate подбирает параметры под эту машину: три прохода, одна полоса,
// наибольшая память из ряда 19, 32, 64, 128, 256 МиБ, при которой вычисление
// укладывается в target. Если даже 19 МиБ медленнее target, возвращаются
// минимальные параметры OWASP.
func Calibrate(target time.Duration) Params {
	best := Params{Time: 3, MemoryKiB: minMemoryKiB, Threads: 1}
	for _, mem := range []uint32{minMemoryKiB, 32 * 1024, 64 * 1024, 128 * 1024, calibrateMaxKiB} {
		p := Params{Time: 3, MemoryKiB: mem, Threads: 1}
		start := time.Now()
		argon2id.Key([]byte("calibration"), []byte("calibration-salt"), nil, nil, p.Time, p.MemoryKiB, p.Threads, derivedKeyLen)
		if time.Since(start) > target {
			break
		}
		best = p
	}
	return best
}
