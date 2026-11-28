package scanner

import (
	"math"
	"strings"
)

// GannLevelTarget represents a single Gann Square of 9 price level.
type GannLevelTarget struct {
	Label string  `json:"label"`
	Angle float64 `json:"angle"`
	Price float64 `json:"price"`
}

// GannLevelsResponse holds computed Gann Square of 9 breakout, support, resistance, and target levels.
type GannLevelsResponse struct {
	Symbol            string            `json:"symbol"`
	ReferenceType     string            `json:"reference_type"` // prev_close, open, custom
	ReferencePrice    float64           `json:"reference_price"`
	LTP               float64           `json:"ltp"`
	ScaleFactor       float64           `json:"scale_factor"`
	Zone              string            `json:"zone"` // ABOVE_BUY, BELOW_SELL, INSIDE_RANGE
	BuyAbove          float64           `json:"buy_above"`
	BuySL             float64           `json:"buy_sl"`
	SellBelow         float64           `json:"sell_below"`
	SellSL            float64           `json:"sell_sl"`
	NearestSupport    float64           `json:"nearest_support"`
	NearestResistance float64           `json:"nearest_resistance"`
	BuyTargets        []GannLevelTarget `json:"buy_targets"`
	SellTargets       []GannLevelTarget `json:"sell_targets"`
}

// gannNormalizeScale maps any price into [100, 10000) spiral space for uniform Square-of-9 steps.
func gannNormalizeScale(price float64) (scaled, factor float64) {
	if price <= 0 {
		return 0, 1
	}
	factor = 1.0
	scaled = price
	for scaled < 100.0 {
		scaled *= 10.0
		factor /= 10.0
	}
	for scaled >= 10000.0 {
		scaled /= 10.0
		factor *= 10.0
	}
	return scaled, factor
}

func roundToTick2(v float64) float64 {
	return math.Round(v*100) / 100
}

// CalculateGannLevels computes W.D. Gann Square of 9 levels using the direct 45-degree table
// (default 5 levels above and 5 levels below the reference/current price).
func CalculateGannLevels(symbol, refType string, refPrice, ltp float64) GannLevelsResponse {
	return CalculateGannLevelsWithCount(symbol, refType, refPrice, ltp, 5)
}

// gannPDFPrice returns the exact Gann Square of 9 table value (0..360 deg in 45-deg steps)
// matching the standard Gann Levels reference table: round((k * 0.125)^2).
func gannPDFPrice(idx int) float64 {
	if idx < 8 {
		idx = 8
	}
	r := float64(idx) * 0.125
	return math.Round(r * r)
}

// CalculateGannLevelsWithCount computes `count` Gann Square of 9 levels above and `count` below refPrice
// using the direct 45-degree Square of 9 price table (1 to 142884+).
func CalculateGannLevelsWithCount(symbol, refType string, refPrice, ltp float64, count int) GannLevelsResponse {
	sym := strings.ToUpper(strings.TrimSpace(symbol))
	if refType == "" {
		refType = "prev_close"
	}
	if count <= 0 {
		count = 5
	}
	if count > 20 {
		count = 20
	}
	if refPrice <= 0 {
		refPrice = ltp
	}
	resp := GannLevelsResponse{
		Symbol:         sym,
		ReferenceType:  refType,
		ReferencePrice: roundToTick2(refPrice),
		LTP:            roundToTick2(ltp),
		ScaleFactor:    1,
		Zone:           "INSIDE_RANGE",
	}
	if refPrice <= 0 {
		return resp
	}

	const step45 = 0.125 // 45 degrees on Square of 9 (8 steps = 360 deg)
	root := math.Sqrt(refPrice)
	k := int(math.Floor(root / step45))

	ku := k
	for gannPDFPrice(ku) <= refPrice {
		ku++
	}
	kl := k
	for kl > 8 && gannPDFPrice(kl) >= refPrice {
		kl--
	}

	for i := 0; i < count; i++ {
		idx := ku + i
		p := gannPDFPrice(idx)
		resp.BuyTargets = append(resp.BuyTargets, GannLevelTarget{
			Label: "R" + itoaSafe(i+1),
			Angle: float64(idx%8) * 45.0,
			Price: p,
		})
	}
	for i := 0; i < count; i++ {
		idx := kl - i
		if idx < 8 {
			break
		}
		p := gannPDFPrice(idx)
		resp.SellTargets = append(resp.SellTargets, GannLevelTarget{
			Label: "S" + itoaSafe(i+1),
			Angle: float64(idx%8) * 45.0,
			Price: p,
		})
	}

	if len(resp.BuyTargets) > 0 {
		resp.BuyAbove = resp.BuyTargets[0].Price
		resp.NearestResistance = resp.BuyAbove
		resp.SellSL = resp.BuyAbove
	}
	if len(resp.SellTargets) > 0 {
		resp.SellBelow = resp.SellTargets[0].Price
		resp.NearestSupport = resp.SellBelow
		resp.BuySL = resp.SellBelow
	}

	if ltp > 0 && resp.BuyAbove > 0 && resp.SellBelow > 0 {
		if ltp > resp.BuyAbove {
			resp.Zone = "ABOVE_BUY"
		} else if ltp < resp.SellBelow {
			resp.Zone = "BELOW_SELL"
		}
	}
	return resp
}
