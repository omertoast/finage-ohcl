package client

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/omertoast/finage/environment"
	"github.com/omertoast/finage/feed"
	"github.com/omertoast/finage/subscriptions"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

var _ feed.Client = &client{}

type (
	client struct {
		connection *websocket.Conn
		tickers    map[string][]opsTicker
		tickersMu  sync.Mutex
	}

	ticker struct {
		Symbol   string  `json:"s"`
		Price    float64 `json:"p"`
		Quantity float64 `json:"q"`
	}

	opsTicker struct {
		Symbol   string `json:"s"`
		Price    string `json:"p"`
		Quantity string `json:"q"`
	}

	subscription struct {
		Action  string `json:"action"`
		Symbols string `json:"symbols"`
	}

	Backends interface {
		Environment() environment.Client
		Subscriptions() subscriptions.Client
	}
)

func New(b Backends) (feed.Client, error) {
	ctx := context.Background() // TODO: make this with context timeout

	finageFeedUrl, err := b.Environment().Get(ctx, "FINAGE_FEED_URL")
	if err != nil {
		return nil, fmt.Errorf("%w %s", feed.ErrInternal, err)
	}

	conn, _, err := websocket.Dial(ctx, finageFeedUrl, nil)
	if err != nil {
		return nil, fmt.Errorf("%w %s", feed.ErrInternal, err)
	}

	c := &client{
		tickers:    make(map[string][]opsTicker),
		connection: conn,
	}

	// read from the websocket connection
	go func() {
		for {
			c.tickersMu.Lock()
			var ticker opsTicker
			err := wsjson.Read(ctx, conn, &ticker)
			if err != nil {
				fmt.Println("error reading message: ", err)
				return
			}

			c.tickers[ticker.Symbol] = append(c.tickers[ticker.Symbol], ticker)

			// fmt.Printf("received message: %v\n", ticker)
			c.tickersMu.Unlock()
		}
	}()

	// send OHCL data to subscribers every second and minute
	// spaghetti alert: this should be reafactored to be dynamic
	go func() {
		secondTicker := time.NewTicker(time.Second)
		minuteTicker := time.NewTicker(time.Minute)
		minuteOhclMap := make(map[string]subscriptions.OHCL)
		var minuteOhclMapMu sync.Mutex

		for {
			select {
			case <-secondTicker.C: // send OHCL data to subscribers every second
				go func() {
					ohclMap := make(map[string]subscriptions.OHCL)
					c.tickersMu.Lock()
					opsTickers := c.tickers
					c.tickers = make(map[string][]opsTicker) // clear the map for the next one second
					c.tickersMu.Unlock()

					for s, ts := range opsTickers {
						ohclMap[s] = tickersToOHCL(ts, "S")
					}

					for s, ohcl := range ohclMap {
						go b.Subscriptions().Publish(ctx, subscriptions.OHCLChannel{Symbol: s, Interval: "S"}, ohcl)
					}

					minuteOhclMapMu.Lock()
					for s, ohcl := range ohclMap {
						if minuteOhclMap[s].S == "" {
							ohcl.I = "M" // change interval to minute so that it can be concatenated
							minuteOhclMap[s] = ohcl
						}

						minuteOhclMap[s] = concactOhcl(minuteOhclMap[s], ohcl)
					}
					minuteOhclMapMu.Unlock()
				}()
			case <-minuteTicker.C: // send OHCL data to subscribers every minute
				go func() {
					minuteOhclMapMu.Lock()
					for s, ohcl := range minuteOhclMap {
						go b.Subscriptions().Publish(ctx, subscriptions.OHCLChannel{Symbol: s, Interval: "M"}, ohcl)
					}

					minuteOhclMap = make(map[string]subscriptions.OHCL) // clear the map for the next one minute
					minuteOhclMapMu.Unlock()
				}()
			}
		}
	}()

	return c, nil
}

// Subscribes to symbols on Finage
func (c *client) Subscribe(ctx context.Context, symbols []string) error {
	err := wsjson.Write(ctx, c.connection, &subscription{
		Action:  "subscribe",
		Symbols: strings.Join(symbols, ","),
	})
	if err != nil {
		return fmt.Errorf("%w %s", feed.ErrInternal, err)
	}

	return nil
}

// Unsubscribes to symbols on Finage
func (c *client) Unsubscribe(ctx context.Context, symbols []string) error {
	err := wsjson.Write(ctx, c.connection, &subscription{
		Action:  "unsubscribe",
		Symbols: strings.Join(symbols, ","),
	})
	if err != nil {
		return fmt.Errorf("%w %s", feed.ErrInternal, err)
	}

	return nil
}

// Create an OHCL data from list of tickers
func tickersToOHCL(opsTickers []opsTicker, interval string) subscriptions.OHCL {
	var ohcl subscriptions.OHCL
	var tickers []ticker

	for _, ot := range opsTickers {
		tickers = append(tickers, opsTickerToTicker(ot))
	}

	ohcl.S = tickers[0].Symbol
	ohcl.O = tickers[0].Price
	ohcl.C = tickers[len(tickers)-1].Price
	ohcl.L = tickers[0].Price

	for _, t := range tickers {
		ohcl.V += t.Quantity
		ohcl.H = max(ohcl.H, t.Price)
		ohcl.L = min(ohcl.L, t.Price)
		ohcl.I = interval
	}

	return ohcl
}

func opsTickerToTicker(ot opsTicker) ticker {
	p, err := strconv.ParseFloat(ot.Price, 64)
	if err != nil {
		fmt.Println("error parsing price: ", err)
	}

	q, err := strconv.ParseFloat(ot.Quantity, 64)
	if err != nil {
		fmt.Println("error parsing quantity: ", err)
	}

	return ticker{
		Symbol:   ot.Symbol,
		Price:    p,
		Quantity: q,
	}
}

// Concatenate two OHCL data
func concactOhcl(prevOhcl, ohcl subscriptions.OHCL) subscriptions.OHCL {
	prevOhcl.C = ohcl.C
	prevOhcl.L = min(prevOhcl.L, ohcl.L)
	prevOhcl.V += ohcl.V
	prevOhcl.H = max(prevOhcl.H, ohcl.H)
	return prevOhcl
}
