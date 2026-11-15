package email

import "context"

// Sender defines the contract for dispatching transactional email messages.
type Sender interface {
	Send(ctx context.Context, msg Message) error
}
