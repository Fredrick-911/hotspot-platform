// Package payment handles M-Pesa STK Push requests and the callbacks
// (webhooks) Safaricom sends when a payment succeeds or fails. Callbacks must
// be processed exactly once, even if Safaricom sends them twice.
//
// Status: planned for Phase 2.
package payment
