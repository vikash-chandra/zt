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

// CalculateGannLevels computes W.D. Gann Square of 9 intraday breakout and target levels.
// Uses 22.5-degree (0.125 sqrt) pivot triggers and 45/90/135/180-degree target steps.
func CalculateGannLevels(symbol, refType string, refPrice, ltp float64) GannLevelsResponse {
	sym := strings.ToUpper(strings.TrimSpace(symbol))
	if refType == "" {
		refType = "prev_close"
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

	scaled, factor := gannNormalizeScale(refPrice)
	resp.ScaleFactor = factor

	root := math.Sqrt(scaled)
	// Base 45-degree grid (0.25 increments in sqrt space) and 22.5-degree inner trigger (0.125)
	const step225 = 0.125
	const step45 = 0.25

	lowerGrid := math.Floor(root/step45) * step45
	upperGrid := lowerGrid + step45
	if math.Abs(root-lowerGrid) < 0.02 {
		upperGrid = lowerGrid + step225
		lowerGrid = lowerGrid - step225
	}

	buyAbove := roundToTick2((upperGrid * upperGrid) * factor)
	sellBelow := roundToTick2((lowerGrid * lowerGrid) * factor)

	resp.BuyAbove = buyAbove
	resp.SellBelow = sellBelow
	resp.BuySL = sellBelow
	resp.SellSL = buyAbove
	resp.NearestResistance = buyAbove
	resp.NearestSupport = sellBelow

	for i := 1; i <= 4; i++ {
		angle := float64(i) * 45.0
		uRoot := upperGrid + float64(i)*step45
		lRoot := math.Max(0.1, lowerGrid-float64(i)*step45)
		resp.BuyTargets = append(resp.BuyTargets, GannLevelTarget{
			Label: "R" + itoaSafe(i),
			Angle: angle,
			Price: roundToTick2((uRoot * uRoot) * factor),
		})
		resp.SellTargets = append(resp.SellTargets, GannLevelTarget{
			Label: "S" + itoaSafe(i),
			Angle: angle,
			Price: roundToTick2((lRoot * lRoot) * factor),
		})
	}

	if ltp > 0 {
		if ltp > buyAbove {
			resp.Zone = "ABOVE_BUY"
			resp.NearestSupport = buyAbove
			for _, t := range resp.BuyTargets {
				if t.Price > ltp {
					resp.NearestResistance = t.Price
					break
				}
			}
		} else if ltp < sellBelow {
			resp.Zone = "BELOW_SELL"
			resp.NearestResistance = sellBelow
			for _, t := range resp.SellTargets {
				if t.Price < ltp {
					resp.NearestSupport = t.Price
					break
				}
			}
		}
	}

	return resp
}
