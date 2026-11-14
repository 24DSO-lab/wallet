package provider

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Тест внутри пакета: ограничитель проверяется на управляемых часах.

func TestLimiterSchedulesSlots(t *testing.T) {
	now := time.Unix(1000, 0)
	var sleeps []time.Duration
	l := newLimiter(100*time.Millisecond, func() time.Time { return now },
		func(_ context.Context, d time.Duration) error { sleeps = append(sleeps, d); return nil })

	for i := 0; i < 4; i++ {
		if err := l.wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	// Часы стоят: слоты выстраиваются 0, 100, 200, 300 мс от первого запроса.
	want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 300 * time.Millisecond}
	if len(sleeps) != len(want) {
		t.Fatalf("sleeps = %v, want %v", sleeps, want)
	}
	for i := range want {
		if sleeps[i] != want[i] {
			t.Errorf("sleep %d = %v, want %v", i, sleeps[i], want[i])
		}
	}

	// После долгой паузы слот снова свободен сразу.
	now = now.Add(time.Hour)
	sleeps = nil
	if err := l.wait(context.Background()); err != nil || len(sleeps) != 0 {
		t.Errorf("idle limiter must not wait: sleeps=%v err=%v", sleeps, err)
	}
}

func TestLimiterDisabled(t *testing.T) {
	l := newLimiter(0, time.Now, func(context.Context, time.Duration) error { t.Error("must not sleep"); return nil })
	for i := 0; i < 5; i++ {
		if err := l.wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLimiterRespectsCanceledContext(t *testing.T) {
	l := newLimiter(0, time.Now, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.wait(ctx); err == nil {
		t.Error("a canceled context must stop the wait")
	}
}

func TestLimiterSlotsAreUniqueUnderConcurrency(t *testing.T) {
	now := time.Unix(0, 0)
	var mu sync.Mutex
	seen := map[time.Duration]bool{}
	l := newLimiter(time.Second, func() time.Time { return now },
		func(_ context.Context, d time.Duration) error {
			mu.Lock()
			defer mu.Unlock()
			if seen[d] {
				t.Errorf("two requests got the same slot %v", d)
			}
			seen[d] = true
			return nil
		})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = l.wait(context.Background()) }()
	}
	wg.Wait()
}
