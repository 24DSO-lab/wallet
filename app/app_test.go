package app

import (
	"fmt"
	"testing"
)

func TestRandomChallengePositions(t *testing.T) {
	for _, wordCount := range []int{12, 24} {
		t.Run(fmt.Sprintf("words-%d", wordCount), func(t *testing.T) {
			for iteration := 0; iteration < 100; iteration++ {
				positions, err := randomChallengePositions(wordCount)
				if err != nil {
					t.Fatalf("randomChallengePositions(%d): %v", wordCount, err)
				}
				if len(positions) != 3 {
					t.Fatalf("got %d challenge positions, want 3", len(positions))
				}
				seen := make(map[int]bool, len(positions))
				for i, position := range positions {
					if position < 0 || position >= wordCount {
						t.Fatalf("position %d is outside 0..%d", position, wordCount-1)
					}
					if seen[position] {
						t.Fatalf("duplicate challenge position %d", position)
					}
					seen[position] = true
					if i > 0 && positions[i-1] >= position {
						t.Fatalf("positions are not strictly sorted: %v", positions)
					}
				}
			}
		})
	}
}

func TestRandomChallengePositionsRejectsTooShortInput(t *testing.T) {
	if _, err := randomChallengePositions(2); err == nil {
		t.Fatal("expected invalid challenge length error")
	}
}

func TestRequireUnlockedDoesNotExtendIdleTimeout(t *testing.T) {
	a := New()
	a.locked = false

	if err := a.requireUnlocked(); err != nil {
		t.Fatalf("requireUnlocked: %v", err)
	}
	if a.lockTimer != nil {
		t.Fatal("background authorization check must not create or reset the idle timer")
	}
}

func TestKeepAliveStartsIdleTimer(t *testing.T) {
	a := New()
	a.locked = false

	if err := a.KeepAlive(); err != nil {
		t.Fatalf("KeepAlive: %v", err)
	}
	if a.lockTimer == nil {
		t.Fatal("foreground activity must start the idle timer")
	}
	a.Lock()
}
