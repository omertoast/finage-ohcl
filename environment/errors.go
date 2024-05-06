package environment

import "errors"

var (
	ErrInternal = errors.New("environment: internal error")
	ErrNotFound = errors.New("environment: not found")
)
