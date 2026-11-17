package exchange

import (
	"math"
)

type Candle struct {
	Timestamp      int64   `json:"timestamp"`
	Open           float64 `json:"open"`
	High           float64 `json:"high"`
	Low            float64 `json:"low"`
	Close          float64 `json:"close"`
	Volume         float64 `json:"volume"`
	TakerBuyVolume float64 `json:"taker_buy_volume"`
}

type MACDResult struct {
	MACD      []float64 `json:"macd"`
	Signal    []float64 `json:"signal"`
	Histogram []float64 `json:"histogram"`
}

type ADXResult struct {
	ADX     []float64 `json:"adx"`
	PlusDI  []float64 `json:"plus_di"`
	MinusDI []float64 `json:"minus_di"`
}

type CVDResult struct {
	Delta []float64 `json:"delta"`
	CVD   []float64 `json:"cvd"`
}

type VPVRBucket struct {
	PriceLow   float64 `json:"price_low"`
	PriceHigh  float64 `json:"price_high"`
	Volume     float64 `json:"volume"`
	BuyVolume  float64 `json:"buy_volume"`
	SellVolume float64 `json:"sell_volume"`
}

type VPVRResult struct {
	Buckets   []VPVRBucket `json:"buckets"`
	POC       float64      `json:"poc"`
	VAH       float64      `json:"vah"`
	VAL       float64      `json:"val"`
	TotalVol  float64      `json:"total_volume"`
}

type FractalType string

const (
	FractalBullish FractalType = "bullish"
	FractalBearish FractalType = "bearish"
)

type Fractal struct {
	Index     int         `json:"index"`
	Timestamp int64       `json:"timestamp"`
	Type      FractalType `json:"type"`
	Price     float64     `json:"price"`
}

type FVGType string

const (
	FVGBullish FVGType = "bullish"
	FVGBearish FVGType = "bearish"
)

type FVG struct {
	Index     int     `json:"index"`
	Timestamp int64   `json:"timestamp"`
	Type      FVGType `json:"type"`
	Top       float64 `json:"top"`
	Bottom    float64 `json:"bottom"`
	Size      float64 `json:"size"`
	Mitigated bool    `json:"mitigated"`
}

type IndicatorReport struct {
	Timeframe string     `json:"timeframe"`
	EMA9      []float64  `json:"ema9"`
	EMA20     []float64  `json:"ema20"`
	EMA50     []float64  `json:"ema50"`
	EMA200    []float64  `json:"ema200"`
	RSI14     []float64  `json:"rsi14"`
	MACD      MACDResult `json:"macd"`
	ADX14     ADXResult  `json:"adx14"`
	ATR14     []float64  `json:"atr14"`
	CVD       CVDResult  `json:"cvd"`
	VPVR      VPVRResult `json:"vpvr"`
	Fractals  []Fractal  `json:"fractals"`
	FVGs      []FVG      `json:"fvgs"`
}

func ComputeEMA(values []float64, period int) []float64 {
	n := len(values)
	out := make([]float64, n)
	if n == 0 || period <= 0 {
		return out
	}
	if n < period {
		copy(out, values)
		return out
	}

	sum := 0.0
	for i := 0; i < period; i++ {
		sum += values[i]
		out[i] = sum / float64(i+1)
	}

	multiplier := 2.0 / float64(period+1)
	out[period-1] = sum / float64(period)

	for i := period; i < n; i++ {
		out[i] = (values[i]-out[i-1])*multiplier + out[i-1]
	}

	return out
}

func ComputeRSI(closes []float64, period int) []float64 {
	n := len(closes)
	out := make([]float64, n)
	if n <= period || period <= 0 {
		for i := range out {
			out[i] = 50.0
		}
		return out
	}

	gains := make([]float64, n)
	losses := make([]float64, n)
	for i := 1; i < n; i++ {
		diff := closes[i] - closes[i-1]
		if diff > 0 {
			gains[i] = diff
		} else {
			losses[i] = -diff
		}
	}

	avgGain := 0.0
	avgLoss := 0.0
	for i := 1; i <= period; i++ {
		avgGain += gains[i]
		avgLoss += losses[i]
		out[i] = 50.0
	}
	avgGain /= float64(period)
	avgLoss /= float64(period)

	if avgLoss == 0 {
		out[period] = 100.0
	} else {
		rs := avgGain / avgLoss
		out[period] = 100.0 - (100.0 / (1.0 + rs))
	}

	for i := period + 1; i < n; i++ {
		avgGain = (avgGain*float64(period-1) + gains[i]) / float64(period)
		avgLoss = (avgLoss*float64(period-1) + losses[i]) / float64(period)

		if avgLoss == 0 {
			out[i] = 100.0
		} else {
			rs := avgGain / avgLoss
			out[i] = 100.0 - (100.0 / (1.0 + rs))
		}
	}

	return out
}

func ComputeMACD(closes []float64, fastPeriod, slowPeriod, signalPeriod int) MACDResult {
	fastEMA := ComputeEMA(closes, fastPeriod)
	slowEMA := ComputeEMA(closes, slowPeriod)

	n := len(closes)
	macdLine := make([]float64, n)
	for i := 0; i < n; i++ {
		macdLine[i] = fastEMA[i] - slowEMA[i]
	}

	signalLine := ComputeEMA(macdLine, signalPeriod)
	histogram := make([]float64, n)
	for i := 0; i < n; i++ {
		histogram[i] = macdLine[i] - signalLine[i]
	}

	return MACDResult{
		MACD:      macdLine,
		Signal:    signalLine,
		Histogram: histogram,
	}
}

func ComputeATR(candles []Candle, period int) []float64 {
	n := len(candles)
	out := make([]float64, n)
	if n == 0 || period <= 0 {
		return out
	}

	tr := make([]float64, n)
	tr[0] = candles[0].High - candles[0].Low
	for i := 1; i < n; i++ {
		h := candles[i].High
		l := candles[i].Low
		cPrev := candles[i-1].Close
		tr1 := h - l
		tr2 := math.Abs(h - cPrev)
		tr3 := math.Abs(l - cPrev)
		tr[i] = math.Max(tr1, math.Max(tr2, tr3))
	}

	if n < period {
		sum := 0.0
		for i := 0; i < n; i++ {
			sum += tr[i]
			out[i] = sum / float64(i+1)
		}
		return out
	}

	sum := 0.0
	for i := 0; i < period; i++ {
		sum += tr[i]
		out[i] = sum / float64(i+1)
	}
	out[period-1] = sum / float64(period)

	for i := period; i < n; i++ {
		out[i] = (out[i-1]*float64(period-1) + tr[i]) / float64(period)
	}

	return out
}

func ComputeADX(candles []Candle, period int) ADXResult {
	n := len(candles)
	res := ADXResult{
		ADX:     make([]float64, n),
		PlusDI:  make([]float64, n),
		MinusDI: make([]float64, n),
	}
	if n <= period || period <= 0 {
		return res
	}

	tr := make([]float64, n)
	plusDM := make([]float64, n)
	minusDM := make([]float64, n)

	tr[0] = candles[0].High - candles[0].Low
	for i := 1; i < n; i++ {
		hDiff := candles[i].High - candles[i-1].High
		lDiff := candles[i-1].Low - candles[i].Low

		if hDiff > 0 && hDiff > lDiff {
			plusDM[i] = hDiff
		}
		if lDiff > 0 && lDiff > hDiff {
			minusDM[i] = lDiff
		}

		h := candles[i].High
		l := candles[i].Low
		cp := candles[i-1].Close
		tr[i] = math.Max(h-l, math.Max(math.Abs(h-cp), math.Abs(l-cp)))
	}

	smoothTR := 0.0
	smoothPDM := 0.0
	smoothMDM := 0.0
	for i := 1; i <= period; i++ {
		smoothTR += tr[i]
		smoothPDM += plusDM[i]
		smoothMDM += minusDM[i]
	}

	dx := make([]float64, n)
	calcDI := func(idx int) {
		if smoothTR == 0 {
			res.PlusDI[idx] = 0
			res.MinusDI[idx] = 0
			dx[idx] = 0
			return
		}
		pDI := 100.0 * (smoothPDM / smoothTR)
		mDI := 100.0 * (smoothMDM / smoothTR)
		res.PlusDI[idx] = pDI
		res.MinusDI[idx] = mDI
		denom := pDI + mDI
		if denom == 0 {
			dx[idx] = 0
		} else {
			dx[idx] = 100.0 * (math.Abs(pDI-mDI) / denom)
		}
	}

	calcDI(period)

	for i := period + 1; i < n; i++ {
		smoothTR = smoothTR - (smoothTR / float64(period)) + tr[i]
		smoothPDM = smoothPDM - (smoothPDM / float64(period)) + plusDM[i]
		smoothMDM = smoothMDM - (smoothMDM / float64(period)) + minusDM[i]
		calcDI(i)
	}

	adxSum := 0.0
	startIdx := period * 2
	if startIdx >= n {
		startIdx = n - 1
	}
	for i := period; i < startIdx; i++ {
		adxSum += dx[i]
	}
	if startIdx > period {
		res.ADX[startIdx-1] = adxSum / float64(startIdx-period)
	}

	for i := startIdx; i < n; i++ {
		prevADX := res.ADX[i-1]
		if prevADX == 0 {
			prevADX = dx[i]
		}
		res.ADX[i] = (prevADX*float64(period-1) + dx[i]) / float64(period)
	}

	return res
}

func ComputeCVD(candles []Candle) CVDResult {
	n := len(candles)
	delta := make([]float64, n)
	cvd := make([]float64, n)

	cumulative := 0.0
	for i := 0; i < n; i++ {
		c := candles[i]
		var d float64
		if c.TakerBuyVolume > 0 {
			takerSell := c.Volume - c.TakerBuyVolume
			if takerSell < 0 {
				takerSell = 0
			}
			d = c.TakerBuyVolume - takerSell
		} else if c.Volume > 0 {
			rng := c.High - c.Low
			if rng > 0 {
				ratio := ((c.Close - c.Low) - (c.High - c.Close)) / rng
				d = c.Volume * ratio
			} else {
				if c.Close >= c.Open {
					d = c.Volume * 0.5
				} else {
					d = -c.Volume * 0.5
				}
			}
		}

		delta[i] = d
		cumulative += d
		cvd[i] = cumulative
	}

	return CVDResult{
		Delta: delta,
		CVD:   cvd,
	}
}

func ComputeVPVR(candles []Candle, numBuckets int) VPVRResult {
	if len(candles) == 0 || numBuckets <= 0 {
		return VPVRResult{}
	}

	minPrice := candles[0].Low
	maxPrice := candles[0].High
	totalVolume := 0.0

	for _, c := range candles {
		if c.Low < minPrice {
			minPrice = c.Low
		}
		if c.High > maxPrice {
			maxPrice = c.High
		}
		totalVolume += c.Volume
	}

	priceRange := maxPrice - minPrice
	if priceRange <= 0 {
		priceRange = 1.0
	}
	bucketSize := priceRange / float64(numBuckets)

	buckets := make([]VPVRBucket, numBuckets)
	for i := 0; i < numBuckets; i++ {
		buckets[i] = VPVRBucket{
			PriceLow:  minPrice + float64(i)*bucketSize,
			PriceHigh: minPrice + float64(i+1)*bucketSize,
		}
	}

	for _, c := range candles {
		cRange := c.High - c.Low
		buyFraction := 0.5
		if c.TakerBuyVolume > 0 && c.Volume > 0 {
			buyFraction = c.TakerBuyVolume / c.Volume
		} else if cRange > 0 {
			buyFraction = math.Max(0.1, math.Min(0.9, (c.Close-c.Low)/cRange))
		}

		if cRange <= 0 {
			idx := int((c.Close - minPrice) / bucketSize)
			if idx >= numBuckets {
				idx = numBuckets - 1
			}
			if idx < 0 {
				idx = 0
			}
			buckets[idx].Volume += c.Volume
			buckets[idx].BuyVolume += c.Volume * buyFraction
			buckets[idx].SellVolume += c.Volume * (1.0 - buyFraction)
			continue
		}

		startIdx := int((c.Low - minPrice) / bucketSize)
		endIdx := int((c.High - minPrice) / bucketSize)
		if startIdx < 0 {
			startIdx = 0
		}
		if endIdx >= numBuckets {
			endIdx = numBuckets - 1
		}

		count := float64(endIdx - startIdx + 1)
		volPerBucket := c.Volume / count
		buyPerBucket := (c.Volume * buyFraction) / count
		sellPerBucket := (c.Volume * (1.0 - buyFraction)) / count

		for i := startIdx; i <= endIdx; i++ {
			buckets[i].Volume += volPerBucket
			buckets[i].BuyVolume += buyPerBucket
			buckets[i].SellVolume += sellPerBucket
		}
	}

	pocIdx := 0
	maxBucketVol := 0.0
	for i, b := range buckets {
		if b.Volume > maxBucketVol {
			maxBucketVol = b.Volume
			pocIdx = i
		}
	}

	pocPrice := (buckets[pocIdx].PriceLow + buckets[pocIdx].PriceHigh) / 2.0
	targetValueVol := totalVolume * 0.70

	includedVol := buckets[pocIdx].Volume
	upIdx := pocIdx + 1
	downIdx := pocIdx - 1
	vahIdx := pocIdx
	valIdx := pocIdx

	for includedVol < targetValueVol && (upIdx < numBuckets || downIdx >= 0) {
		upVol := 0.0
		downVol := 0.0
		if upIdx < numBuckets {
			upVol = buckets[upIdx].Volume
		}
		if downIdx >= 0 {
			downVol = buckets[downIdx].Volume
		}

		if upVol >= downVol && upIdx < numBuckets {
			includedVol += upVol
			vahIdx = upIdx
			upIdx++
		} else if downIdx >= 0 {
			includedVol += downVol
			valIdx = downIdx
			downIdx--
		} else if upIdx < numBuckets {
			includedVol += upVol
			vahIdx = upIdx
			upIdx++
		}
	}

	return VPVRResult{
		Buckets:  buckets,
		POC:      pocPrice,
		VAH:      buckets[vahIdx].PriceHigh,
		VAL:      buckets[valIdx].PriceLow,
		TotalVol: totalVolume,
	}
}

func ComputeFractals(candles []Candle) []Fractal {
	n := len(candles)
	var fractals []Fractal
	if n < 5 {
		return fractals
	}

	for i := 2; i < n-2; i++ {
		c := candles[i]
		if c.High > candles[i-1].High && c.High > candles[i-2].High &&
			c.High > candles[i+1].High && c.High > candles[i+2].High {
			fractals = append(fractals, Fractal{
				Index:     i,
				Timestamp: c.Timestamp,
				Type:      FractalBearish,
				Price:     c.High,
			})
		}
		if c.Low < candles[i-1].Low && c.Low < candles[i-2].Low &&
			c.Low < candles[i+1].Low && c.Low < candles[i+2].Low {
			fractals = append(fractals, Fractal{
				Index:     i,
				Timestamp: c.Timestamp,
				Type:      FractalBullish,
				Price:     c.Low,
			})
		}
	}

	return fractals
}

func ComputeFVGs(candles []Candle) []FVG {
	n := len(candles)
	var fvgs []FVG
	if n < 3 {
		return fvgs
	}

	for i := 2; i < n; i++ {
		cCurrent := candles[i]
		cPast := candles[i-2]

		if cCurrent.Low > cPast.High {
			gapSize := cCurrent.Low - cPast.High
			fvg := FVG{
				Index:     i - 1,
				Timestamp: candles[i-1].Timestamp,
				Type:      FVGBullish,
				Top:       cCurrent.Low,
				Bottom:    cPast.High,
				Size:      gapSize,
				Mitigated: false,
			}
			for k := i + 1; k < n; k++ {
				if candles[k].Low <= fvg.Top {
					fvg.Mitigated = true
					break
				}
			}
			fvgs = append(fvgs, fvg)
		} else if cCurrent.High < cPast.Low {
			gapSize := cPast.Low - cCurrent.High
			fvg := FVG{
				Index:     i - 1,
				Timestamp: candles[i-1].Timestamp,
				Type:      FVGBearish,
				Top:       cPast.Low,
				Bottom:    cCurrent.High,
				Size:      gapSize,
				Mitigated: false,
			}
			for k := i + 1; k < n; k++ {
				if candles[k].High >= fvg.Bottom {
					fvg.Mitigated = true
					break
				}
			}
			fvgs = append(fvgs, fvg)
		}
	}

	return fvgs
}

func ComputeAllIndicators(timeframe string, candles []Candle) *IndicatorReport {
	closes := make([]float64, len(candles))
	for i, c := range candles {
		closes[i] = c.Close
	}

	return &IndicatorReport{
		Timeframe: timeframe,
		EMA9:      ComputeEMA(closes, 9),
		EMA20:     ComputeEMA(closes, 20),
		EMA50:     ComputeEMA(closes, 50),
		EMA200:    ComputeEMA(closes, 200),
		RSI14:     ComputeRSI(closes, 14),
		MACD:      ComputeMACD(closes, 12, 26, 9),
		ADX14:     ComputeADX(candles, 14),
		ATR14:     ComputeATR(candles, 14),
		CVD:       ComputeCVD(candles),
		VPVR:      ComputeVPVR(candles, 50),
		Fractals:  ComputeFractals(candles),
		FVGs:      ComputeFVGs(candles),
	}
}

func ComputeCVDDivergence(spotCandles, perpCandles []Candle) []float64 {
	spotCVD := ComputeCVD(spotCandles)
	perpCVD := ComputeCVD(perpCandles)

	minLen := len(spotCVD.CVD)
	if len(perpCVD.CVD) < minLen {
		minLen = len(perpCVD.CVD)
	}

	div := make([]float64, minLen)
	for i := 0; i < minLen; i++ {
		div[i] = perpCVD.CVD[i] - spotCVD.CVD[i]
	}
	return div
}
