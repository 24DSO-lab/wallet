package provider

import (
	"context"
	"sync"
	"time"
)

// limiter выдерживает наименьший интервал между началами запросов. Ожидание
// прерывается контекстом. Нулевой интервал отключает ограничение.
type limiter struct {
	interval time.Duration
	now      func() time.Time
	sleep    func(context.Context, time.Duration) error

	mu   sync.Mutex
	next time.Time // момент, раньше которого следующий запрос стартовать нельзя
}

func newLimiter(interval time.Duration, now func() time.Time, sleep func(context.Context, time.Duration) error) *limiter {
	return &limiter{interval: interval, now: now, sleep: sleep}
}

// wait резервирует слот и ждёт его наступления. Слоты выдаются по очереди,
// поэтому горутины не обгоняют друг друга.
func (l *limiter) wait(ctx context.Context) error {
	if l.interval <= 0 {
		return ctx.Err()
	}
	l.mu.Lock()
	now := l.now()
	start := l.next
	if start.Before(now) {
		start = now
	}
	l.next = start.Add(l.interval)
	l.mu.Unlock()

	if d := start.Sub(now); d > 0 {
		return l.sleep(ctx, d)
	}
	return ctx.Err()
}
