package subscriptions

import (
	"context"
	"net/http"
)

type Client interface {
	Publish(ctx context.Context, channel OHCLChannel, data OHCL) error
	ServeHTTP(w http.ResponseWriter, r *http.Request)
}
