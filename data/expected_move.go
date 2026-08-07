package data

import (
	"math"
	"time"
)

// ExpectedMoveResult holds the quantitative expected range calculations
type ExpectedMoveResult struct {
	IndexSymbol          string              `json:"index_symbol"`
	SpotPrice            float64             `json:"spot_price"`
	VIX                  float64             `json:"vix"`
	VIXExpectedMove      VIXMoveDetails      `json:"vix_expected_move"`
	StraddleExpectedMove StraddleMoveDetails `json:"straddle_expected_move"`
	ContractSensitivity  ContractDetails     `json:"contract_sensitivity"`
	CalculatedAt         time.Time           `json:"calculated_at"`
}

type VIXMoveDetails struct {
	DailyPoints     float64 `json:"daily_points"`
	DailyPct        float64 `json:"daily_pct"`
	RemainingPoints float64 `json:"remaining_points"`
	UpperBound      float64 `json:"upper_bound"`
	LowerBound      float64 `json:"lower_bound"`
}

type StraddleMoveDetails struct {
	ATMStrike      float64 `json:"atm_strike"`
	StraddlePrice  float64 `json:"straddle_price"`
	ExpectedPoints float64 `json:"expected_points"`
	UpperBound     float64 `json:"upper_bound"`
	LowerBound     float64 `json:"lower_bound"`
}

type ContractDetails struct {
	Contract          string             `json:"contract"`
	CurrentPremium    float64            `json:"current_premium"`
	Delta             float64            `json:"delta"`
	ThetaPerHour      float64            `json:"theta_per_hour"`
	ProjectedPremiums ProjectedMoveTable `json:"projected_premiums"`
}

type ProjectedMoveTable struct {
	Plus50Pts   float64 `json:"plus_50pts"`
	Plus100Pts  float64 `json:"plus_100pts"`
	Minus50Pts  float64 `json:"minus_50pts"`
	Minus100Pts float64 `json:"minus_100pts"`
}

// CalculateExpectedMove computes mathematical range targets using VIX, Straddle, and Delta
func CalculateExpectedMove(spot float64, vix float64, straddlePrice float64, contractSym string, contractLtp float64, strike float64, isCall bool, now time.Time) ExpectedMoveResult {
	nowIST := NormalizeToIST(now)

	// 1. VIX Expected Move (calculated strictly when spot > 0 and vix > 0 from DB or Zerodha)
	dailyPct := 0.0
	dailyPoints := 0.0
	remainingPoints := 0.0
	vixUpper := spot
	vixLower := spot

	if vix > 0 && spot > 0 {
		dailyPct = (vix / math.Sqrt(365.0)) / 100.0
		dailyPoints = spot * dailyPct

		marketOpen := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), 9, 15, 0, 0, ISTLocation)
		marketClose := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), 15, 30, 0, 0, ISTLocation)

		hrsRemaining := 6.25
		if nowIST.After(marketOpen) && nowIST.Before(marketClose) {
			hrsRemaining = marketClose.Sub(nowIST).Hours()
		} else if nowIST.After(marketClose) {
			hrsRemaining = 0.0
		}
		if hrsRemaining > 6.25 {
			hrsRemaining = 6.25
		}

		remainingPoints = dailyPoints * math.Sqrt(hrsRemaining/6.25)
		vixUpper = spot + dailyPoints
		vixLower = spot - dailyPoints
	}

	// 2. ATM Straddle Expected Move (calculated strictly when straddlePrice > 0 from DB or Zerodha)
	atmStrike := 0.0
	expectedStraddlePoints := 0.0
	straddleUpper := spot
	straddleLower := spot

	if spot > 0 {
		atmStrike = math.Round(spot/50.0) * 50.0
	}

	if straddlePrice > 0 && spot > 0 {
		expectedStraddlePoints = 0.85 * straddlePrice
		straddleUpper = spot + expectedStraddlePoints
		straddleLower = spot - expectedStraddlePoints
	}

	// 3. Option Contract Sensitivity (calculated strictly when contractLtp > 0 from DB or Zerodha)
	delta := 0.0
	thetaPerHour := 0.0
	plus50 := 0.0
	plus100 := 0.0
	minus50 := 0.0
	minus100 := 0.0

	if spot > 0 && strike > 0 {
		distFromStrike := spot - strike
		if !isCall {
			distFromStrike = strike - spot
		}

		delta = 0.50
		if distFromStrike < -300 {
			delta = 0.12
		} else if distFromStrike < -200 {
			delta = 0.20
		} else if distFromStrike < -100 {
			delta = 0.35
		} else if distFromStrike > 300 {
			delta = 0.88
		} else if distFromStrike > 200 {
			delta = 0.80
		} else if distFromStrike > 100 {
			delta = 0.65
		}

		if contractLtp > 0 {
			marketClose := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), 15, 30, 0, 0, ISTLocation)
			hrsRemaining := 6.25
			if nowIST.Before(marketClose) {
				hrsRemaining = marketClose.Sub(nowIST).Hours()
			}
			if hrsRemaining < 0.5 {
				hrsRemaining = 0.5
			}
			if hrsRemaining > 6.25 {
				hrsRemaining = 6.25
			}

			thetaPerHour = -(contractLtp * 0.04) / hrsRemaining

			plus50 = contractLtp + (50.0 * delta)
			plus100 = contractLtp + (100.0 * delta)
			minus50 = math.Max(0.05, contractLtp-(50.0*delta))
			minus100 = math.Max(0.05, contractLtp-(100.0*delta))

			if !isCall {
				plus50 = math.Max(0.05, contractLtp-(50.0*delta))
				plus100 = math.Max(0.05, contractLtp-(100.0*delta))
				minus50 = contractLtp + (50.0 * delta)
				minus100 = contractLtp + (100.0 * delta)
			}
		}
	}

	return ExpectedMoveResult{
		IndexSymbol: "NIFTY 50",
		SpotPrice:   spot,
		VIX:         vix,
		VIXExpectedMove: VIXMoveDetails{
			DailyPoints:     math.Round(dailyPoints*100) / 100,
			DailyPct:        math.Round(dailyPct*10000) / 100,
			RemainingPoints: math.Round(remainingPoints*100) / 100,
			UpperBound:      math.Round(vixUpper*100) / 100,
			LowerBound:      math.Round(vixLower*100) / 100,
		},
		StraddleExpectedMove: StraddleMoveDetails{
			ATMStrike:      atmStrike,
			StraddlePrice:  math.Round(straddlePrice*100) / 100,
			ExpectedPoints: math.Round(expectedStraddlePoints*100) / 100,
			UpperBound:     math.Round(straddleUpper*100) / 100,
			LowerBound:     math.Round(straddleLower*100) / 100,
		},
		ContractSensitivity: ContractDetails{
			Contract:       contractSym,
			CurrentPremium: math.Round(contractLtp*100) / 100,
			Delta:          math.Round(delta*100) / 100,
			ThetaPerHour:   math.Round(thetaPerHour*100) / 100,
			ProjectedPremiums: ProjectedMoveTable{
				Plus50Pts:   math.Round(plus50*100) / 100,
				Plus100Pts:  math.Round(plus100*100) / 100,
				Minus50Pts:  math.Round(minus50*100) / 100,
				Minus100Pts: math.Round(minus100*100) / 100,
			},
		},
		CalculatedAt: nowIST,
	}
}
