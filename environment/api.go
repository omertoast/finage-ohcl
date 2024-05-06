package environment

import "context"

type Client interface {
	Get(ctx context.Context, name string) (string, error)
}
