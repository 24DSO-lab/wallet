// Package processlock prevents multiple Wallet processes from opening the same
// profile directory concurrently. The lock file is intentionally persistent:
// deleting a locked file can let another process lock a different inode.
package processlock

import "errors"

// ErrAlreadyRunning indicates that another Wallet process owns the data directory.
var ErrAlreadyRunning = errors.New("another Wallet instance is already running")
