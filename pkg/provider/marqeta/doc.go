// Package marqeta is a placeholder adapter. Not production-ready. See https://www.marqeta.com/docs/core-api.
//
// No method of the Provider interface is implemented. Every call returns
// provider.ErrNotSupported wrapped with a "not implemented" message. The
// package is kept compiled so the import path remains stable and a real
// implementation can drop into the same path in a follow-up. The env-gated
// registration in cmd/treasuryd/main.go has been replaced with a warning
// log line; no fake provider is ever registered.
package marqeta
