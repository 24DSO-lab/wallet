package service

import (
	"sync"
	"testing"
)

func TestLockDataWriteUsesSharedSnapshotMutex(t *testing.T) {
	mu := &sync.RWMutex{}
	s := &Service{dataMu: mu}

	unlock, err := s.lockDataWrite()
	if err != nil {
		t.Fatalf("lockDataWrite: %v", err)
	}
	if mu.TryLock() {
		mu.Unlock()
		t.Fatal("write lock did not acquire the shared data mutex")
	}
	unlock()
	if !mu.TryLock() {
		t.Fatal("write lock was not released")
	}
	mu.Unlock()
}

func TestLockDataWriteWithoutSharedMutexIsSafe(t *testing.T) {
	var s Service
	unlock, err := s.lockDataWrite()
	if err != nil {
		t.Fatalf("lockDataWrite: %v", err)
	}
	unlock()
}
