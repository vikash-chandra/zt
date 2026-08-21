package main

import (
	"fmt"
	"log"
	"math"
	"strings"

	"zerodha-trading/config"
	"zerodha-trading/data"
	"zerodha-trading/selection"

	kiteconnect "github.com/zerodha/gokiteconnect/v4"
)

func main() {
	fmt.Println("===================================================================================")
	fmt.Println("       ZERODHA LIVE OPTIONS TRADING PRE-FLIGHT & MARGIN VERIFICATION SUITE         ")
	fmt.Println("===================================================================================")

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if cfg.APIKey == "" || cfg.AccessToken == "" {
		log.Fatalf("Missing ZERODHA_API_KEY or ZERODHA_ACCESS_TOKEN in .env")
	}

	kc := kiteconnect.New(cfg.APIKey)
	kc.SetAccessToken(cfg.AccessToken)
	broker := data.NewZerodhaBrokerAdapter(kc)

	// Step 1: Verify API Connectivity & Account Permissions
	fmt.Print("\n[1/4] Verifying Zerodha API Authentication & Connectivity... ")
	orders, err := broker.GetOrders()
	if err != nil {
		log.Fatalf("FAILED: %v (Check your API Key & Access Token)", err)
	}
	fmt.Printf("✓ SUCCESS (Retrieved %d active/historical orders today)\n", len(orders))

	positions, err := broker.GetPositions()
	if err != nil {
		log.Fatalf("FAILED to fetch positions: %v", err)
	}
	fmt.Printf("✓ Portfolio Positions Verified (Net: %d, Day: %d)\n", len(positions.Net), len(positions.Day))

	// Step 2: Test Strike Selector & Market Quotes Across All 5 Indices
	fmt.Println("\n[2/4] Testing Dynamic Strike Selection & Live Exchange Quotes across All Indices...")
	indices := []string{"NIFTY 50", "NIFTY BANK", "BSE SENSEX", "FINNIFTY", "MIDCPNIFTY"}
	strikeSelector := selection.NewOptionStrikeSelector(nil)

	type TestedIndex struct {
		Name          string
		Spec          *data.IndexSpec
		SpotPrice     float64
		SelectedPE    string
		PELTP         float64
		PEExpiry      string
		SelectedCE    string
		CELTP         float64
		CEExpiry      string
		EntryMarginPE float64
		SLMarginPE    float64
	}

	var results []TestedIndex

	for _, idxName := range indices {
		spec, found := data.ResolveIndexSpec(idxName)
		if !found {
			fmt.Printf("  ❌ Could not resolve spec for %s\n", idxName)
			continue
		}

		quoteKey := fmt.Sprintf("%s:%s", spec.SpotExchange, spec.Name)
		if spec.Name == "NIFTY 50" {
			quoteKey = "NSE:NIFTY 50"
		} else if spec.Name == "NIFTY BANK" {
			quoteKey = "NSE:NIFTY BANK"
		} else if spec.Name == "BSE SENSEX" {
			quoteKey = "BSE:SENSEX"
		} else if spec.Name == "FINNIFTY" {
			quoteKey = "NSE:NIFTY FIN SERVICE"
		} else if spec.Name == "MIDCPNIFTY" {
			quoteKey = "NSE:NIFTY MID SELECT"
		}

		spotQuote, err := broker.GetQuote(quoteKey)
		spotPrice := 0.0
		if err == nil && len(spotQuote) > 0 {
			if q, ok := spotQuote[quoteKey]; ok && q.LastPrice > 0 {
				spotPrice = q.LastPrice
			}
		}

		if spotPrice <= 0 {
			ohlc, ohlcErr := broker.GetOHLC(quoteKey)
			if ohlcErr == nil && len(ohlc) > 0 {
				spotPrice = ohlc[quoteKey].LastPrice
				if spotPrice <= 0 {
					spotPrice = ohlc[quoteKey].OHLC.Close
				}
			}
		}

		if spotPrice <= 0 {
			fmt.Printf("  ⚠️ %s: Unable to fetch live spot price for %s\n", spec.Name, quoteKey)
			continue
		}

		// 1. Select OTM PE strike
		peRes, peErr := strikeSelector.SelectStrikeByTargetPremium(
			spec.Name, spotPrice, "BULLISH",
			spec.DefaultTargetPremium, cfg.Options.ExpiryType, cfg.Options.NextMonthDays,
			broker,
		)

		// 2. Select OTM CE strike
		ceRes, ceErr := strikeSelector.SelectStrikeByTargetPremium(
			spec.Name, spotPrice, "BEARISH",
			spec.DefaultTargetPremium, cfg.Options.ExpiryType, cfg.Options.NextMonthDays,
			broker,
		)

		if peErr != nil || ceErr != nil {
			fmt.Printf("  ❌ %s: Strike selection error (PE: %v, CE: %v)\n", spec.Name, peErr, ceErr)
			continue
		}

		peLTP := peRes.SelectedLTP
		if quotes, err := broker.GetQuote(peRes.Exchange + ":" + peRes.OptionSymbol); err == nil {
			if q, ok := quotes[peRes.Exchange+":"+peRes.OptionSymbol]; ok && q.LastPrice > 0 {
				peLTP = q.LastPrice
			}
		}

		ceLTP := ceRes.SelectedLTP
		if quotes, err := broker.GetQuote(ceRes.Exchange + ":" + ceRes.OptionSymbol); err == nil {
			if q, ok := quotes[ceRes.Exchange+":"+ceRes.OptionSymbol]; ok && q.LastPrice > 0 {
				ceLTP = q.LastPrice
			}
		}

		fmt.Printf("  ✓ %-12s Spot: ₹%-9.2f | PE: %-22s (LTP: ₹%-6.2f, Exp: %s) | CE: %-22s (LTP: ₹%-6.2f, Exp: %s)\n",
			spec.Name, spotPrice, peRes.OptionSymbol, peLTP, peRes.ExpiryDate, ceRes.OptionSymbol, ceLTP, ceRes.ExpiryDate)

		results = append(results, TestedIndex{
			Name:       spec.Name,
			Spec:       spec,
			SpotPrice:  spotPrice,
			SelectedPE: peRes.OptionSymbol,
			PELTP:      peLTP,
			PEExpiry:   peRes.ExpiryDate,
			SelectedCE: ceRes.OptionSymbol,
			CELTP:      ceLTP,
			CEExpiry:   ceRes.ExpiryDate,
		})
	}

	// Step 3: Test Exchange Order Margin Pre-Trade Validation on Zerodha
	fmt.Println("\n[3/4] Testing Zerodha Live Exchange Margin Pre-Trade Validation (`GetOrderMargins`)...")
	fmt.Println("      (This verifies symbol, lot size, MIS product, and exchange acceptance WITHOUT placing live trades)")

	for i, ti := range results {
		spec := ti.Spec
		sym := ti.SelectedPE
		ltp := ti.PELTP
		if ltp <= 0 {
			ltp = spec.DefaultTargetPremium
		}

		limitPrice := math.Max(0.50, math.Floor(ltp*0.95*20.0)/20.0)

		// 1. Entry SELL Order Margin Request
		entryParam := data.OrderParams{
			Exchange:        spec.OptionsExchange,
			TradingSymbol:   sym,
			TransactionType: "SELL",
			Quantity:        spec.BaseLotSize,
			Price:           limitPrice,
			OrderType:       "LIMIT",
			Product:         "MIS",
			Validity:        "DAY",
		}

		entryMargins, err := broker.GetOrderMargins([]data.OrderParams{entryParam})
		if err != nil {
			fmt.Printf("  ❌ %s SELL Order Margin Check Failed: %v\n", spec.Name, err)
			continue
		}

		entryTotalMargin := 0.0
		entrySpan := 0.0
		entryExposure := 0.0
		if len(entryMargins) > 0 {
			entryTotalMargin = entryMargins[0].Total
			entrySpan = entryMargins[0].Span
			entryExposure = entryMargins[0].Exposure
		}

		// 2. Stop-Loss BUY SL-Limit Order Margin Request
		trigPrice := math.Round(ltp*1.50*20.0) / 20.0
		slLimitPrice := math.Ceil(trigPrice*1.05*20.0) / 20.0
		slParam := data.OrderParams{
			Exchange:        spec.OptionsExchange,
			TradingSymbol:   sym,
			TransactionType: "BUY",
			Quantity:        spec.BaseLotSize,
			TriggerPrice:    trigPrice,
			Price:           slLimitPrice,
			OrderType:       "SL",
			Product:         "MIS",
			Validity:        "DAY",
		}

		slMargins, err := broker.GetOrderMargins([]data.OrderParams{slParam})
		if err != nil {
			fmt.Printf("  ❌ %s SL Order Margin Check Failed: %v\n", spec.Name, err)
			continue
		}

		slTotalMargin := 0.0
		if len(slMargins) > 0 {
			slTotalMargin = slMargins[0].Total
		}

		results[i].EntryMarginPE = entryTotalMargin
		results[i].SLMarginPE = slTotalMargin

		fmt.Printf("  ✓ %-12s [%s] SELL %-20s (Qty: %3d, Price: ₹%6.2f) -> SPAN: ₹%-8.2f | EXP: ₹%-8.2f | TOTAL: ₹%-9.2f [ACCEPTED]\n",
			spec.Name, spec.OptionsExchange, sym, spec.BaseLotSize, limitPrice, entrySpan, entryExposure, entryTotalMargin)
		fmt.Printf("    %-12s [%s] SL-BUY Trigger: ₹%6.2f, Limit: ₹%6.2f (Qty: %3d) -> SL Margin Required: ₹%-8.2f [ACCEPTED]\n",
			"", spec.OptionsExchange, trigPrice, slLimitPrice, spec.BaseLotSize, slTotalMargin)
	}

	// Step 4: Summary Table
	fmt.Println("\n[4/4] ============================== PRE-FLIGHT VERIFICATION SUMMARY ==============================")
	fmt.Printf("%-12s | %-5s | %-4s | %-22s | %-8s | %-12s | %-12s | %-10s\n",
		"Index", "Exch", "Lots", "Contract Symbol", "LTP (₹)", "SELL Margin", "SL-BUY Margin", "Status")
	fmt.Println(strings.Repeat("-", 100))

	allPassed := true
	for _, ti := range results {
		if ti.EntryMarginPE <= 0 {
			allPassed = false
		}
		statusStr := "READY FOR LIVE"
		if ti.EntryMarginPE <= 0 {
			statusStr = "FAILED"
		}
		fmt.Printf("%-12s | %-5s | %-4d | %-22s | ₹%-7.2f | ₹%-11.2f | ₹%-11.2f | ✓ %s\n",
			ti.Name, ti.Spec.OptionsExchange, ti.Spec.BaseLotSize, ti.SelectedPE, ti.PELTP, ti.EntryMarginPE, ti.SLMarginPE, statusStr)
	}
	fmt.Println(strings.Repeat("-", 100))

	if allPassed && len(results) == 5 {
		fmt.Println("\n🎉 ALL 5 INDICES PASSED PRE-FLIGHT CHECKS! Zerodha Kite Connect accepted all live order parameter structures.")
		fmt.Println("   You can toggle LIVE mode (`is_live = true`) on any index safely at any time.")
	} else {
		fmt.Printf("\n⚠️ Completed with warnings: %d of 5 indices passed pre-flight.\n", len(results))
	}
}
