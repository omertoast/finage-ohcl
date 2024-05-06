package client

import (
	"context"
	"fmt"
	"os"

	"github.com/omertoast/finage/environment"

	"github.com/joho/godotenv"
)

var _ environment.Client = client{}

type client struct{}

func New() environment.Client {
	err := godotenv.Load(".env")
	if err != nil {
		fmt.Println("environment: Warning: .env file not found.")
	}

	return &client{}
}

func (c client) Get(ctx context.Context, name string) (string, error) {
	value := os.Getenv(name)
	if value == "" {
		return "", fmt.Errorf("%s %v", environment.ErrNotFound, name)
	}

	return value, nil
}
