package payment

import "errors"

var (
	ErrReservationNotFound         = errors.New("payment: reservation not found")
	ErrReservationForbidden        = errors.New("payment: reservation does not belong to customer")
	ErrReservationNotHeld          = errors.New("payment: reservation is not in held status")
	ErrReservationExpired          = errors.New("payment: reservation has expired")
	ErrFreeReservationNotSupported = errors.New("payment: free reservations cannot use checkout payment")

	ErrPaymentAttemptNotFound = errors.New("payment: payment attempt not found")
	ErrCheckoutInProgress     = errors.New("payment: checkout session initialization is in progress")
	ErrProviderSessionFailed  = errors.New("payment: failed to create provider checkout session")
	ErrAmountMismatch         = errors.New("payment: webhook amount does not match payment attempt")
	ErrCurrencyMismatch       = errors.New("payment: webhook currency does not match payment attempt")
	ErrInvalidStateTransition = errors.New("payment: invalid payment state transition")
	ErrOverRefund             = errors.New("payment: refund amount exceeds collected payment amount")

	ErrInvalidSignature = errors.New("payment: invalid or missing webhook signature")
	ErrStaleTimestamp   = errors.New("payment: webhook timestamp outside acceptable window")
	ErrInvalidPayload   = errors.New("payment: invalid webhook payload")
)
