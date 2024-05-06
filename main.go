package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/omertoast/finage/environment"
	env_client "github.com/omertoast/finage/environment/client"
	"github.com/omertoast/finage/feed"
	feed_client "github.com/omertoast/finage/feed/client"
	"github.com/omertoast/finage/subscriptions"
	subs_client "github.com/omertoast/finage/subscriptions/client"
)

func main() {
	b := &backends{}
	log.SetFlags(0)

	b.environment = env_client.New()
	b.subscriptions = subs_client.New(b)

	feedClient, err := feed_client.New(b)
	if err != nil {
		log.Fatalf("failed to create feed client: %v", err)
	}
	b.feed = feedClient

	err = run(*b)
	if err != nil {
		log.Fatal(err)
	}
}

// run initializes the chatServer and then
// starts a http.Server for the passed in address.
func run(b backends) error {
	if len(os.Args) < 2 {
		return errors.New("please provide an address to listen on as the first argument")
	}

	l, err := net.Listen("tcp", os.Args[1])
	if err != nil {
		return err
	}
	log.Printf("listening on ws://%v", l.Addr())

	s := &http.Server{
		Handler:      b.subscriptions,
		ReadTimeout:  time.Second * 10,
		WriteTimeout: time.Second * 10,
	}
	errc := make(chan error, 1)
	go func() {
		errc <- s.Serve(l)
	}()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt)
	select {
	case err := <-errc:
		log.Printf("failed to serve: %v", err)
	case sig := <-sigs:
		log.Printf("terminating: %v", sig)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	return s.Shutdown(ctx)
}

type backends struct {
	environment   environment.Client
	subscriptions subscriptions.Client
	feed          feed.Client
}

func (b backends) Environment() environment.Client {
	return b.environment
}

func (b backends) Subscriptions() subscriptions.Client {
	return b.subscriptions
}

func (b backends) Feed() feed.Client {
	return b.feed
}
