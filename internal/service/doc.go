// Package service implements application use cases over internal/ports.
//
// It owns orchestration, transaction boundaries, wallet-count enforcement and
// the security-sensitive order of send operations. It does not define Wails
// request/response DTOs; the app facade must map these operations only after
// the public application contract has been reviewed.
package service
