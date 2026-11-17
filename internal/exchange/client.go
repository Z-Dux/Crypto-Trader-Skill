package exchange

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	ccxt "github.com/ccxt/ccxt/go/v4"
	"github.com/gorilla/websocket"
	"golang.org/x/sync/errgroup"
)

type Client struct {
	spotExchange *ccxt.Binance
	perpExchange *ccxt.Binance
	wsListener   *WebSocketListener
	httpClient   *http.Client
}

type ClientConfig struct {
	APIKey string
	Secret string
}

func NewClient(cfg ClientConfig) *Client {
	spotConfig := map[string]any{
		"options": map[string]any{
			"defaultType": "spot",
		},
	}
	perpConfig := map[string]any{
		"options": map[string]any{
			"defaultType": "swap",
		},
	}

	if cfg.APIKey != "" {
		spotConfig["apiKey"] = cfg.APIKey
		spotConfig["secret"] = cfg.Secret
		perpConfig["apiKey"] = cfg.APIKey
		perpConfig["secret"] = cfg.Secret
	}

	return &Client{
		spotExchange: ccxt.NewBinance(spotConfig),
		perpExchange: ccxt.NewBinance(perpConfig),
		wsListener:   NewWebSocketListener(),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (c *Client) SpotExchange() *ccxt.Binance {
	return c.spotExchange
}

func (c *Client) PerpExchange() *ccxt.Binance {
	return c.perpExchange
}

func (c *Client) WebSocketListener() *WebSocketListener {
	return c.wsListener
}

type MarketSnapshot struct {
	Symbol          string                      `json:"symbol"`
	Timeframes      map[string][]Candle         `json:"timeframes"`
	OpenInterest    []ccxt.OpenInterest         `json:"open_interest,omitempty"`
	FundingRates    []ccxt.FundingRateHistory   `json:"funding_rates,omitempty"`
	LongShortRatios []ccxt.LongShortRatio       `json:"long_short_ratios,omitempty"`
	OrderBook       *ccxt.OrderBook             `json:"order_book,omitempty"`
	Indicators      map[string]*IndicatorReport `json:"indicators,omitempty"`
}

func (c *Client) FetchParallelMarketData(ctx context.Context, symbol string) (*MarketSnapshot, error) {
	timeframes := []string{"1m", "5m", "1h", "4h"}
	snapshot := &MarketSnapshot{
		Symbol:     symbol,
		Timeframes: make(map[string][]Candle),
		Indicators: make(map[string]*IndicatorReport),
	}

	var mu sync.Mutex
	g, gCtx := errgroup.WithContext(ctx)

	for _, tf := range timeframes {
		timeframe := tf
		g.Go(func() error {
			candles, err := c.FetchKlinesWithTaker(gCtx, true, symbol, timeframe, 250)
			if err != nil {
				return fmt.Errorf("failed fetching klines for %s: %w", timeframe, err)
			}
			report := ComputeAllIndicators(timeframe, candles)

			mu.Lock()
			snapshot.Timeframes[timeframe] = candles
			snapshot.Indicators[timeframe] = report
			mu.Unlock()
			return nil
		})
	}

	g.Go(func() error {
		oi, err := c.perpExchange.FetchOpenInterestHistory(
			symbol,
			ccxt.WithFetchOpenInterestHistoryTimeframe("1h"),
			ccxt.WithFetchOpenInterestHistoryLimit(30),
		)
		if err == nil {
			mu.Lock()
			snapshot.OpenInterest = oi
			mu.Unlock()
		}
		return nil
	})

	g.Go(func() error {
		fr, err := c.perpExchange.FetchFundingRateHistory(
			ccxt.WithFetchFundingRateHistorySymbol(symbol),
			ccxt.WithFetchFundingRateHistoryLimit(30),
		)
		if err == nil {
			mu.Lock()
			snapshot.FundingRates = fr
			mu.Unlock()
		}
		return nil
	})

	g.Go(func() error {
		lsr, err := c.perpExchange.FetchLongShortRatioHistory(
			ccxt.WithFetchLongShortRatioHistorySymbol(symbol),
			ccxt.WithFetchLongShortRatioHistoryTimeframe("1h"),
			ccxt.WithFetchLongShortRatioHistoryLimit(30),
		)
		if err == nil {
			mu.Lock()
			snapshot.LongShortRatios = lsr
			mu.Unlock()
		}
		return nil
	})

	g.Go(func() error {
		ob, err := c.perpExchange.FetchOrderBook(
			symbol,
			ccxt.WithFetchOrderBookLimit(50),
		)
		if err == nil {
			mu.Lock()
			snapshot.OrderBook = &ob
			mu.Unlock()
		}
		return nil
	})

	if err := g.Wait(); err != nil {
		return nil, err
	}

	return snapshot, nil
}

func (c *Client) FetchKlinesWithTaker(ctx context.Context, isPerp bool, symbol, timeframe string, limit int) ([]Candle, error) {
	rawSymbol := NormalizeRawSymbol(symbol)
	baseURL := "https://fapi.binance.com/fapi/v1/klines"
	if !isPerp {
		baseURL = "https://api.binance.com/api/v3/klines"
	}

	url := fmt.Sprintf("%s?symbol=%s&interval=%s&limit=%d", baseURL, rawSymbol, timeframe, limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return c.fallbackFetchOHLCV(symbol, timeframe, limit)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.fallbackFetchOHLCV(symbol, timeframe, limit)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var rawRows [][]any
	if err := json.Unmarshal(body, &rawRows); err != nil {
		return nil, err
	}

	candles := make([]Candle, 0, len(rawRows))
	for _, row := range rawRows {
		if len(row) < 10 {
			continue
		}
		openTime, _ := toInt64(row[0])
		open, _ := toFloat64(row[1])
		high, _ := toFloat64(row[2])
		low, _ := toFloat64(row[3])
		closeVal, _ := toFloat64(row[4])
		vol, _ := toFloat64(row[5])
		takerBuyVol, _ := toFloat64(row[9])

		candles = append(candles, Candle{
			Timestamp:      openTime,
			Open:           open,
			High:           high,
			Low:            low,
			Close:          closeVal,
			Volume:         vol,
			TakerBuyVolume: takerBuyVol,
		})
	}

	return candles, nil
}

func (c *Client) fallbackFetchOHLCV(symbol, timeframe string, limit int) ([]Candle, error) {
	ohlcvList, err := c.perpExchange.FetchOHLCV(
		symbol,
		ccxt.WithFetchOHLCVTimeframe(timeframe),
		ccxt.WithFetchOHLCVLimit(int64(limit)),
	)
	if err != nil {
		return nil, err
	}

	candles := make([]Candle, len(ohlcvList))
	for i, o := range ohlcvList {
		candles[i] = Candle{
			Timestamp:      o.Timestamp,
			Open:           o.Open,
			High:           o.High,
			Low:            o.Low,
			Close:          o.Close,
			Volume:         o.Volume,
			TakerBuyVolume: o.Volume * 0.5,
		}
	}
	return candles, nil
}

func NormalizeRawSymbol(symbol string) string {
	s := strings.ToUpper(symbol)
	s = strings.ReplaceAll(s, "/", "")
	s = strings.ReplaceAll(s, ":USDT", "")
	return s
}

func toFloat64(v any) (float64, error) {
	switch val := v.(type) {
	case float64:
		return val, nil
	case string:
		return strconv.ParseFloat(val, 64)
	default:
		return 0, fmt.Errorf("cannot convert %T to float64", v)
	}
}

func toInt64(v any) (int64, error) {
	switch val := v.(type) {
	case int64:
		return val, nil
	case float64:
		return int64(val), nil
	case string:
		return strconv.ParseInt(val, 10, 64)
	default:
		return 0, fmt.Errorf("cannot convert %T to int64", v)
	}
}

type TradeEvent struct {
	Timestamp int64   `json:"timestamp"`
	Price     float64 `json:"price"`
	Quantity  float64 `json:"quantity"`
	IsBuyer   bool    `json:"is_buyer"`
}

type LiquidationEvent struct {
	Timestamp int64   `json:"timestamp"`
	Symbol    string  `json:"symbol"`
	Side      string  `json:"side"`
	Price     float64 `json:"price"`
	Quantity  float64 `json:"quantity"`
	VolumeUSD float64 `json:"volume_usd"`
}

type TapeSummary struct {
	Timestamp        int64   `json:"timestamp"`
	TradeCount       int     `json:"trade_count"`
	TotalVolume      float64 `json:"total_volume"`
	BuyVolume        float64 `json:"buy_volume"`
	SellVolume       float64 `json:"sell_volume"`
	Delta            float64 `json:"delta"`
	LongLiqVolumeUSD float64 `json:"long_liq_volume_usd"`
	ShortLiqVolumeUSD float64 `json:"short_liq_volume_usd"`
}

type WebSocketListener struct {
	mu           sync.RWMutex
	conn         *websocket.Conn
	trades       []TradeEvent
	liquidations []LiquidationEvent
	running      bool
	stopChan     chan struct{}
}

func NewWebSocketListener() *WebSocketListener {
	return &WebSocketListener{
		trades:       make([]TradeEvent, 0, 1000),
		liquidations: make([]LiquidationEvent, 0, 500),
		stopChan:     make(chan struct{}),
	}
}

func (w *WebSocketListener) Start(ctx context.Context, symbol string) error {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return nil
	}
	w.running = true
	w.stopChan = make(chan struct{})
	w.mu.Unlock()

	rawSymbol := strings.ToLower(NormalizeRawSymbol(symbol))
	streamURL := fmt.Sprintf("wss://fstream.binancefuture.com/stream?streams=%s@aggTrade/%s@forceOrder", rawSymbol, rawSymbol)

	go w.listenLoop(ctx, streamURL)
	return nil
}

func (w *WebSocketListener) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.running {
		return
	}
	w.running = false
	close(w.stopChan)
	if w.conn != nil {
		_ = w.conn.Close()
	}
}

func (w *WebSocketListener) listenLoop(ctx context.Context, url string) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopChan:
			return
		default:
		}

		header := http.Header{}
		header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
		conn, _, err := websocket.DefaultDialer.Dial(url, header)
		if err != nil {
			select {
			case <-time.After(2 * time.Second):
				continue
			case <-w.stopChan:
				return
			case <-ctx.Done():
				return
			}
		}

		w.mu.Lock()
		w.conn = conn
		w.mu.Unlock()

		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				_ = conn.Close()
				break
			}
			w.handleMessage(msg)
		}
	}
}

type binanceWSStreamPayload struct {
	Stream string          `json:"stream"`
	Data   json.RawMessage `json:"data"`
}

type binanceWSTrade struct {
	EventType string `json:"e"`
	EventTime int64  `json:"E"`
	Price     string `json:"p"`
	Quantity  string `json:"q"`
	IsBuyer   bool   `json:"m"`
}

type binanceWSForceOrder struct {
	EventType string `json:"e"`
	EventTime int64  `json:"E"`
	Order     struct {
		Symbol           string `json:"s"`
		Side             string `json:"S"`
		Price            string `json:"p"`
		OriginalQuantity string `json:"q"`
		TradeTime        int64  `json:"T"`
	} `json:"o"`
}

func (w *WebSocketListener) handleMessage(msg []byte) {
	var payload binanceWSStreamPayload
	if err := json.Unmarshal(msg, &payload); err != nil {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	now := time.Now().UnixMilli()
	cutoff := now - 60000

	if strings.Contains(payload.Stream, "@trade") || strings.Contains(payload.Stream, "@aggTrade") {
		var tradeData binanceWSTrade
		if err := json.Unmarshal(payload.Data, &tradeData); err == nil {
			p, _ := strconv.ParseFloat(tradeData.Price, 64)
			q, _ := strconv.ParseFloat(tradeData.Quantity, 64)
			isBuy := !tradeData.IsBuyer

			w.trades = append(w.trades, TradeEvent{
				Timestamp: tradeData.EventTime,
				Price:     p,
				Quantity:  q,
				IsBuyer:   isBuy,
			})
		}
	} else if strings.Contains(payload.Stream, "@forceOrder") {
		var fo binanceWSForceOrder
		if err := json.Unmarshal(payload.Data, &fo); err == nil {
			p, _ := strconv.ParseFloat(fo.Order.Price, 64)
			q, _ := strconv.ParseFloat(fo.Order.OriginalQuantity, 64)

			w.liquidations = append(w.liquidations, LiquidationEvent{
				Timestamp: fo.Order.TradeTime,
				Symbol:    fo.Order.Symbol,
				Side:      fo.Order.Side,
				Price:     p,
				Quantity:  q,
				VolumeUSD: p * q,
			})
		}
	}

	validTrades := w.trades[:0]
	for _, t := range w.trades {
		if t.Timestamp >= cutoff {
			validTrades = append(validTrades, t)
		}
	}
	w.trades = validTrades

	validLiqs := w.liquidations[:0]
	for _, l := range w.liquidations {
		if l.Timestamp >= cutoff {
			validLiqs = append(validLiqs, l)
		}
	}
	w.liquidations = validLiqs
}

func (w *WebSocketListener) WatchTrades() []TradeEvent {
	w.mu.RLock()
	defer w.mu.RUnlock()
	res := make([]TradeEvent, len(w.trades))
	copy(res, w.trades)
	return res
}

func (w *WebSocketListener) WatchLiquidations() []LiquidationEvent {
	w.mu.RLock()
	defer w.mu.RUnlock()
	res := make([]LiquidationEvent, len(w.liquidations))
	copy(res, w.liquidations)
	return res
}

func (w *WebSocketListener) Get1mRealtimeTape() TapeSummary {
	w.mu.RLock()
	defer w.mu.RUnlock()

	summary := TapeSummary{
		Timestamp:  time.Now().UnixMilli(),
		TradeCount: len(w.trades),
	}

	for _, t := range w.trades {
		vol := t.Price * t.Quantity
		summary.TotalVolume += vol
		if t.IsBuyer {
			summary.BuyVolume += vol
		} else {
			summary.SellVolume += vol
		}
	}
	summary.Delta = summary.BuyVolume - summary.SellVolume

	for _, l := range w.liquidations {
		if l.Side == "SELL" {
			summary.LongLiqVolumeUSD += l.VolumeUSD
		} else {
			summary.ShortLiqVolumeUSD += l.VolumeUSD
		}
	}

	return summary
}
