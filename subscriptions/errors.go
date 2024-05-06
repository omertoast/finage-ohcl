package subscriptions

import "errors"

var (
	ErrInternal = errors.New("subscriptions: internal error")
	ErrNotFound = errors.New("subscriptions: not found")
)
