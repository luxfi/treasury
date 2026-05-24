// Package mpesa is the Safaricom M-Pesa Daraja adapter for treasuryd.
// It implements Provider for B2C payouts, C2B STK Push pay-ins, account
// balance queries, and transaction status lookup. FX, counterparty
// registration, and standard CancelPayment are unsupported by Daraja's
// model: FX is handled by forexd, payees are addressed by msisdn at
// send time, and reversals are a privileged operation exposed via the
// adapter's Reverse method rather than the Provider interface.
//
// Daraja API reference: https://developer.safaricom.co.ke/Documentation
package mpesa
