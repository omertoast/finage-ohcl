package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/omertoast/finage/feed"
	"github.com/omertoast/finage/subscriptions"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

var _ subscriptions.Client = &client{}

const (
	ActionSubscribe   ActionType = "subscribe"
	ActionUnsubscribe ActionType = "unsubscribe"
)

// client enables broadcasting to a set of subscribers.
type (
	client struct {
		// subscriberMessageBuffer controls the max number
		// of messages that can be queued for a subscriber
		// before it is kicked.
		//
		// Defaults to 16.
		subscriberMessageBuffer int

		// logf controls where logs are sent.
		// Defaults to log.Printf.
		logf func(f string, v ...interface{})

		// serveMux routes the various endpoints to the appropriate handler.
		serveMux http.ServeMux

		subscribersMu sync.Mutex
		subscribers   map[*subscriber]struct{}

		b Backends
	}

	Backends interface {
		Feed() feed.Client
	}

	ActionType string

	Payload struct {
		Action  ActionType `json:"action"`
		Symbols []string   `json:"symbols"`
		Time    string     `json:"time"`
	}

	// subscriber represents a subscriber.
	// Messages are sent on the msgs channel and if the client
	// cannot keep up with the messages, closeSlow is called.
	subscriber struct {
		channels  []subscriptions.OHCLChannel
		msgs      chan []byte
		closeSlow func()
	}
)

func New(b Backends) *client {
	c := &client{
		subscriberMessageBuffer: 16,
		logf:                    log.Printf,
		subscribers:             make(map[*subscriber]struct{}),
		b:                       b,
	}
	c.serveMux.HandleFunc("/", c.subscribeHandler)

	return c
}

func (c *client) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.serveMux.ServeHTTP(w, r)
}

// subscribeHandler accepts the WebSocket connection and then subscribes
// it to all future messages.
func (c *client) subscribeHandler(w http.ResponseWriter, r *http.Request) {
	err := c.subscribe(r.Context(), w, r)
	if errors.Is(err, context.Canceled) {
		return
	}
	if websocket.CloseStatus(err) == websocket.StatusNormalClosure ||
		websocket.CloseStatus(err) == websocket.StatusGoingAway {
		return
	}
	if err != nil {
		c.logf("%v", err)
		return
	}
}

// subscribe subscribes the given WebSocket to all broadcast messages.
// It creates a subscriber with a buffered msgs chan to give some room to slower
// connections and then registers the subscriber. It then listens for all messages
// and writes them to the WebSocket. If the context is cancelled or
// an error occurs, it returns and deletes the subscription.
//
// It uses CloseRead to keep reading from the connection to process control
// messages and cancel the context if the connection drops.
func (c *client) subscribe(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	var mu sync.Mutex
	var conn *websocket.Conn
	var closed bool
	s := &subscriber{
		channels: []subscriptions.OHCLChannel{},
		msgs:     make(chan []byte, c.subscriberMessageBuffer),
		closeSlow: func() {
			mu.Lock()
			defer mu.Unlock()
			closed = true
			if conn != nil {
				conn.Close(websocket.StatusPolicyViolation, "connection too slow to keep up with messages")
			}
		},
	}
	c.addSubscriber(s)
	defer func() {
		err := c.deleteSubscriber(ctx, s)
		if err != nil {
			fmt.Println("failed to delete subscriber: ", err)
		}
	}()

	c2, err := websocket.Accept(w, r, nil)
	if err != nil {
		return err
	}
	mu.Lock()
	if closed {
		mu.Unlock()
		return net.ErrClosed
	}
	conn = c2
	mu.Unlock()
	defer conn.CloseNow()

	// read from the websocket connection
	go func() {
		for {
			var payload Payload
			err = wsjson.Read(ctx, conn, &payload)
			if err != nil {
				fmt.Println(err)
				return
			}

			var interval string
			switch payload.Time {
			case "M":
				interval = "M"
			case "S":
				interval = "S"
			default:
				fmt.Println("invalid interval: ", payload.Time)
				return
			}

			channels := make([]subscriptions.OHCLChannel, len(payload.Symbols))
			for i, symbol := range payload.Symbols {
				channels[i] = subscriptions.OHCLChannel{
					Symbol:   symbol,
					Interval: interval,
				}
			}

			switch payload.Action {
			case ActionSubscribe:
				c.addChannels(ctx, s, channels)
			case ActionUnsubscribe:
				c.deleteChannels(ctx, s, channels)
			default:
				fmt.Println("invalid action: ", payload.Action)
				return
			}
		}
	}()

	// write to the websocket connection
	for {
		select {
		case msg := <-s.msgs:
			err := writeTimeout(ctx, time.Second*5, conn, msg)
			if err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// publish publishes the msg to all subscribers.
// It never blocks and so messages to slow subscribers
// are dropped.
func (c *client) Publish(ctx context.Context, channel subscriptions.OHCLChannel, data subscriptions.OHCL) error {
	c.subscribersMu.Lock()
	defer c.subscribersMu.Unlock()

	msg, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("%w %s", subscriptions.ErrInternal, err)
	}

	go func() {
		for s := range c.subscribers {
			for _, ch := range s.channels {
				if ch == channel {
					select {
					case s.msgs <- msg:
					default:
						go s.closeSlow()
					}
				}
			}
		}
	}()

	return nil
}

func (c *client) addChannels(ctx context.Context, s *subscriber, channels []subscriptions.OHCLChannel) error {
	c.subscribersMu.Lock()
	defer c.subscribersMu.Unlock()

	var subSymbols []string
	for _, channel := range channels {
		var exists bool
		for _, c := range s.channels {
			if c == channel {
				exists = true
				break
			}
		}
		if !exists {
			s.channels = append(s.channels, channel)
		}

		subSymbols = append(subSymbols, channel.Symbol)
	}

	err := c.b.Feed().Subscribe(ctx, subSymbols)
	if err != nil {
		return fmt.Errorf("%w %s", subscriptions.ErrInternal, err)
	}

	return nil
}

func (c *client) deleteChannels(ctx context.Context, s *subscriber, channels []subscriptions.OHCLChannel) error {
	c.subscribersMu.Lock()
	defer c.subscribersMu.Unlock()

	for _, channel := range channels {
		for i, c := range s.channels {
			if c == channel {
				s.channels = append(s.channels[:i], s.channels[i+1:]...)
				break
			}
		}
	}

	var unSubSymbols []string
	for _, channel := range channels {
		var subscribed bool
		for s := range c.subscribers {
			for _, c := range s.channels {
				if c == channel {
					subscribed = true
					break
				}
			}
		}
		if !subscribed {
			unSubSymbols = append(unSubSymbols, channel.Symbol)
		}
	}

	err := c.b.Feed().Unsubscribe(ctx, unSubSymbols)
	if err != nil {
		fmt.Println("failed to unsubscribe: ", err)
		return fmt.Errorf("%w %s", subscriptions.ErrInternal, err)
	}

	return nil
}

func (c *client) addSubscriber(s *subscriber) {
	c.subscribersMu.Lock()
	defer c.subscribersMu.Unlock()
	c.subscribers[s] = struct{}{}
}

func (c *client) deleteSubscriber(ctx context.Context, s *subscriber) error {
	tmpChannels := slices.Clone(s.channels)
	err := c.deleteChannels(ctx, s, tmpChannels)
	if err != nil {
		return fmt.Errorf("%w %s", subscriptions.ErrInternal, err)
	}

	c.subscribersMu.Lock()
	defer c.subscribersMu.Unlock()

	delete(c.subscribers, s)

	return nil
}

func writeTimeout(ctx context.Context, timeout time.Duration, c *websocket.Conn, msg []byte) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return c.Write(ctx, websocket.MessageText, msg)
}
