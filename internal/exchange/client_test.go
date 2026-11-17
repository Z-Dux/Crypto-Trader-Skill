package exchange

import (
	"context"
	"strconv"
	"testing"
	"time"
)

func TestCCXTInstantiation(t *testing.T) {
	client := NewClient(ClientConfig{})
	if client.SpotExchange() == nil {
		t.Fatal("failed to instantiate spotExchange")
	}
	if client.PerpExchange() == nil {
		t.Fatal("failed to instantiate perpExchange")
	}
}

func TestIndicators(t *testing.T) {
	candles := make([]Candle, 50)
	basePrice := 60000.0

	for i := 0; i < 50; i++ {
		open := basePrice + float64(i)*20.0
		high := open + 50.0
		low := open - 30.0
		closeVal := open + 10.0
		vol := 100.0 + float64(i)*5.0
		takerBuy := vol * 0.55

		if i == 10 {
			high = open + 200.0
		}
		if i == 20 {
			low = open - 200.0
		}
		if i == 15 {
			low = open + 60.0
		}

		candles[i] = Candle{
			Timestamp:      time.Now().UnixMilli() + int64(i*60000),
			Open:           open,
			High:           high,
			Low:            low,
			Close:          closeVal,
			Volume:         vol,
			TakerBuyVolume: takerBuy,
		}
	}

	closes := make([]float64, len(candles))
	for i, c := range candles {
		closes[i] = c.Close
	}

	ema := ComputeEMA(closes, 9)
	if len(ema) != len(closes) {
		t.Fatalf("expected EMA len %d, got %d", len(closes), len(ema))
	}
	if ema[len(ema)-1] <= 0 {
		t.Errorf("expected positive EMA, got %f", ema[len(ema)-1])
	}

	rsi := ComputeRSI(closes, 14)
	if len(rsi) != len(closes) {
		t.Fatalf("expected RSI len %d, got %d", len(closes), len(rsi))
	}
	for i := 14; i < len(rsi); i++ {
		if rsi[i] < 0 || rsi[i] > 100 {
			t.Errorf("RSI out of bounds [0, 100]: %f at index %d", rsi[i], i)
		}
	}

	macd := ComputeMACD(closes, 12, 26, 9)
	if len(macd.MACD) != len(closes) || len(macd.Signal) != len(closes) || len(macd.Histogram) != len(closes) {
		t.Fatalf("MACD array lengths mismatch")
	}

	atr := ComputeATR(candles, 14)
	if len(atr) != len(candles) {
		t.Fatalf("expected ATR len %d, got %d", len(candles), len(atr))
	}
	if atr[len(atr)-1] <= 0 {
		t.Errorf("expected positive ATR, got %f", atr[len(atr)-1])
	}

	adx := ComputeADX(candles, 14)
	if len(adx.ADX) != len(candles) {
		t.Fatalf("expected ADX len %d, got %d", len(candles), len(adx.ADX))
	}

	cvd := ComputeCVD(candles)
	if len(cvd.CVD) != len(candles) {
		t.Fatalf("expected CVD len %d, got %d", len(candles), len(cvd.CVD))
	}

	vpvr := ComputeVPVR(candles, 30)
	if len(vpvr.Buckets) != 30 {
		t.Fatalf("expected 30 VPVR buckets, got %d", len(vpvr.Buckets))
	}
	if vpvr.POC <= 0 {
		t.Errorf("expected positive POC, got %f", vpvr.POC)
	}
	if vpvr.VAH < vpvr.VAL {
		t.Errorf("expected VAH >= VAL, got VAH=%f VAL=%f", vpvr.VAH, vpvr.VAL)
	}

	_ = ComputeFractals(candles)
	_ = ComputeFVGs(candles)

	report := ComputeAllIndicators("1m", candles)
	if report == nil {
		t.Fatal("expected non-nil indicator report")
	}
	if len(report.EMA9) != len(candles) || len(report.RSI14) != len(candles) {
		t.Error("report indicator slices length mismatch")
	}

	div := ComputeCVDDivergence(candles, candles)
	if len(div) != len(candles) {
		t.Fatalf("expected divergence length %d, got %d", len(candles), len(div))
	}
}

func TestWebSocketTape(t *testing.T) {
	listener := NewWebSocketListener()

	nowStr := strconv.FormatInt(time.Now().UnixMilli(), 10)
	tradePayload := `{"stream":"btcusdt@aggTrade","data":{"e":"aggTrade","E":` + nowStr + `,"p":"65000.5","q":"0.5","m":false}}`
	listener.handleMessage([]byte(tradePayload))

	trades := listener.WatchTrades()
	if len(trades) != 1 {
		t.Fatalf("expected 1 trade, got %d", len(trades))
	}
	if trades[0].Price != 65000.5 || trades[0].Quantity != 0.5 || !trades[0].IsBuyer {
		t.Errorf("trade values mismatch: %+v", trades[0])
	}

	liqPayload := `{"stream":"btcusdt@forceOrder","data":{"e":"forceOrder","E":` + nowStr + `,"o":{"s":"BTCUSDT","S":"SELL","p":"64000.0","q":"1.5","T":` + nowStr + `}}}`
	listener.handleMessage([]byte(liqPayload))

	liqs := listener.WatchLiquidations()
	if len(liqs) != 1 {
		t.Fatalf("expected 1 liquidation, got %d", len(liqs))
	}
	if liqs[0].Price != 64000.0 || liqs[0].Quantity != 1.5 || liqs[0].Side != "SELL" {
		t.Errorf("liquidation values mismatch: %+v", liqs[0])
	}

	tape := listener.Get1mRealtimeTape()
	if tape.TradeCount != 1 {
		t.Errorf("expected 1 trade count, got %d", tape.TradeCount)
	}
	if tape.LongLiqVolumeUSD <= 0 {
		t.Errorf("expected positive long liq volume, got %f", tape.LongLiqVolumeUSD)
	}
	if tape.BuyVolume <= 0 {
		t.Errorf("expected positive buy volume, got %f", tape.BuyVolume)
	}
}

func TestLiveWebSocketTape(t *testing.T) {
	listener := NewWebSocketListener()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := listener.Start(ctx, "BTC/USDT")
	if err != nil {
		t.Fatalf("Start err: %v", err)
	}
	defer listener.Stop()

	time.Sleep(2 * time.Second)

	tape := listener.Get1mRealtimeTape()
	t.Logf("Live tape: trade_count=%d total_volume=%f delta=%f", tape.TradeCount, tape.TotalVolume, tape.Delta)
	if tape.TradeCount == 0 {
		t.Fatalf("expected live trade_count > 0, got %d", tape.TradeCount)
	}
	if tape.TotalVolume <= 0 {
		t.Fatalf("expected live volume > 0, got %f", tape.TotalVolume)
	}
}

