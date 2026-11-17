package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"trader-skill/internal/exchange"
)

type Output struct {
	MarketSnapshot *exchange.MarketSnapshot `json:"market_snapshot"`
	RealtimeTape   exchange.TapeSummary     `json:"realtime_tape"`
}

func main() {
	symbol := "BTC/USDT"
	if len(os.Args) > 1 && os.Args[1] != "" {
		symbol = os.Args[1]
	}

	client := exchange.NewClient(exchange.ClientConfig{})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	ws := client.WebSocketListener()
	_ = ws.Start(ctx, symbol)
	defer ws.Stop()

	snapshot, err := client.FetchParallelMarketData(ctx, symbol)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	time.Sleep(2 * time.Second)

	tape := ws.Get1mRealtimeTape()

	out := Output{
		MarketSnapshot: snapshot,
		RealtimeTape:   tape,
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "json error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(string(data))
}
