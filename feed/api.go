package feed

import (
	"context"
)

type Client interface {
	Subscribe(ctx context.Context, symbols []string) error
	Unsubscribe(ctx context.Context, symbols []string) error
}
