package feed

import "errors"

var (
	ErrInternal = errors.New("feed: internal error")
	ErrNotFound = errors.New("feed: not found")
)
