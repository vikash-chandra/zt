package scanner

import (
	"math"
	"strings"
)

// Round100LevelsResponse holds multiples-of-100 psychological levels for index trading strategies and chart overlays.
type Round100LevelsResponse struct {
	Symbol         string    `json:"symbol"`
	IsIndex        bool      `json:"is_index"`
	ReferencePrice float64   `json:"reference_price"`
	LTP            float64   `json:"ltp"`
	NearestBelow   float64   `json:"nearest_below"`
	NearestAbove   float64   `json:"nearest_above"`
	AboveLevels    []float64 `json:"above_levels"`
	BelowLevels    []float64 `json:"below_levels"`
}

// IsIndexForRound100 returns true if symbol is a supported Indian equity index.
func IsIndexForRound100(symbol string) bool {
	u := strings.ToUpper(strings.TrimSpace(symbol))
	switch u {
	case "NIFTY", "NIFTY 50",
		"BANKNIFTY", "BANK NIFTY", "NIFTY BANK",
		"SENSEX", "BSE SENSEX",
		"FINNIFTY", "NIFTY FIN SERVICE",
		"MIDCP", "MIDCPNIFTY", "NIFTY MID SELECT",
		"BANKEX":
		return true
	default:
		return false
	}
}

// NearestRound100Levels returns the immediate 100-multiple strictly below and strictly above price.
func NearestRound100Levels(price float64) (below, above float64) {
	if price <= 0 {
		return 0, 0
	}
	base := math.Floor(price/100.0) * 100.0
	if base == price {
		return math.Max(0, base-100.0), base + 100.0
	}
	return base, base + 100.0
}

// CalculateRound100Levels computes `count` multiples of 100 above and below refPrice for indices.
// Intended for both chart overlays and future index breakout/reversal strategies.
func CalculateRound100Levels(symbol string, refPrice, ltp float64, count int) Round100LevelsResponse {
	sym := strings.ToUpper(strings.TrimSpace(symbol))
	if count <= 0 {
		count = 20
	}
	if count > 50 {
		count = 50
	}
	if refPrice <= 0 {
		refPrice = ltp
	}
	resp := Round100LevelsResponse{
		Symbol:         sym,
		IsIndex:        IsIndexForRound100(sym),
		ReferencePrice: roundToTick2(refPrice),
		LTP:            roundToTick2(ltp),
	}
	if !resp.IsIndex || refPrice <= 0 {
		return resp
	}

	below, above := NearestRound100Levels(refPrice)
	resp.NearestBelow = below
	resp.NearestAbove = above

	for i := 0; i < count; i++ {
		resp.AboveLevels = append(resp.AboveLevels, above+float64(i)*100.0)
	}
	for i := 0; i < count; i++ {
		lvl := below - float64(i)*100.0
		if lvl <= 0 {
			break
		}
		resp.BelowLevels = append(resp.BelowLevels, lvl)
	}
	return resp
}
