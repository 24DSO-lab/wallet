// Package securestore stores provider credentials using the current OS user account.
package securestore

import "errors"

// ErrUnavailable means this platform has no supported OS-backed secret store.
var ErrUnavailable = errors.New("securestore: OS secret storage is unavailable")
