package main

import (
	"testing"

	"zerodha-trading/config"
	"zerodha-trading/monitoring"
	"zerodha-trading/strategy"
)

// TestStrategyRequireIFPValidation tests that IsStrategyRequireIFP returns true
// when configured for each of the 5 strategies, and false otherwise.
func TestStrategyRequireIFPValidation(t *testing.T) {
	logger, err := monitoring.NewLogger("info")
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}

	bot := &TradingBot{
		cfg:    &config.Settings{},
		logger: logger,
	}

	strategies := []string{
		"LOW_VOLUME",
		"VANDE_BHARAT",
		"FAKE_BREAKOUT",
		"VANDE_BHARAT_TRAP",
		"EMAS5_BREAKOUT",
	}

	// Initially all should be false
	for _, strat := range strategies {
		if bot.IsStrategyRequireIFP(strat) {
			t.Errorf("expected IsStrategyRequireIFP(%s) to be false by default", strat)
		}
	}

	// Enable IFP requirement for VANDE_BHARAT and EMAS5_BREAKOUT
	bot.strategyRequireIFPMapMutex.Lock()
	if bot.strategyRequireIFPMap == nil {
		bot.strategyRequireIFPMap = make(map[string]bool)
	}
	bot.strategyRequireIFPMap["VANDE_BHARAT"] = true
	bot.strategyRequireIFPMap["EMAS5_BREAKOUT"] = true
	bot.strategyRequireIFPMapMutex.Unlock()

	if !bot.IsStrategyRequireIFP("VANDE_BHARAT") {
		t.Errorf("expected IsStrategyRequireIFP(VANDE_BHARAT) to be true")
	}
	if !bot.IsStrategyRequireIFP("EMAS5_BREAKOUT") {
		t.Errorf("expected IsStrategyRequireIFP(EMAS5_BREAKOUT) to be true")
	}
	if bot.IsStrategyRequireIFP("LOW_VOLUME") {
		t.Errorf("expected IsStrategyRequireIFP(LOW_VOLUME) to be false")
	}
	if bot.IsStrategyRequireIFP("FAKE_BREAKOUT") {
		t.Errorf("expected IsStrategyRequireIFP(FAKE_BREAKOUT) to be false")
	}
	if bot.IsStrategyRequireIFP("VANDE_BHARAT_TRAP") {
		t.Errorf("expected IsStrategyRequireIFP(VANDE_BHARAT_TRAP) to be false")
	}
}

// TestHasSymbolIFP tests all resolution paths for HasSymbolIFP:
// 1. Symbol has "IFP" in symbolProvenance slice
// 2. Symbol has "IFP" in watchlistSelectorMap
// 3. Symbol has compound selector "FO,IFP" in watchlistSelectorMap
// 4. Symbol has neither (returns false)
func TestHasSymbolIFP(t *testing.T) {
	logger, _ := monitoring.NewLogger("info")

	bot := &TradingBot{
		cfg:                  &config.Settings{},
		logger:               logger,
		symbolProvenance:     make(map[string][]string),
		watchlistSelectorMap: make(map[string]string),
	}

	// Case 1: Symbol not present anywhere
	if bot.HasSymbolIFP("RELIANCE") {
		t.Errorf("expected HasSymbolIFP(RELIANCE) to be false when no tags exist")
	}

	// Case 2: Symbol has only "FO" tag in symbolProvenance
	bot.symbolProvenanceMutex.Lock()
	bot.symbolProvenance["TCS"] = []string{"FO", "SECTOR"}
	bot.symbolProvenanceMutex.Unlock()
	if bot.HasSymbolIFP("TCS") {
		t.Errorf("expected HasSymbolIFP(TCS) to be false with only FO,SECTOR")
	}

	// Case 3: Symbol has "IFP" added to symbolProvenance
	bot.symbolProvenanceMutex.Lock()
	bot.symbolProvenance["TCS"] = append(bot.symbolProvenance["TCS"], "IFP")
	bot.symbolProvenanceMutex.Unlock()
	if !bot.HasSymbolIFP("TCS") {
		t.Errorf("expected HasSymbolIFP(TCS) to be true after adding IFP to provenance")
	}

	// Case 4: Symbol in watchlistSelectorMap with single tag "IFP"
	bot.watchlistSelectorMapMutex.Lock()
	bot.watchlistSelectorMap["INFY"] = "IFP"
	bot.watchlistSelectorMapMutex.Unlock()
	if !bot.HasSymbolIFP("INFY") {
		t.Errorf("expected HasSymbolIFP(INFY) to be true from watchlistSelectorMap")
	}

	// Case 5: Symbol in watchlistSelectorMap with compound tag "NEWS,IFP,PDH_PDL"
	bot.watchlistSelectorMapMutex.Lock()
	bot.watchlistSelectorMap["HDFCBANK"] = "NEWS,IFP,PDH_PDL"
	bot.watchlistSelectorMapMutex.Unlock()
	if !bot.HasSymbolIFP("HDFCBANK") {
		t.Errorf("expected HasSymbolIFP(HDFCBANK) to be true from compound selector tag")
	}

	// Case 6: Symbol with compound tag without IFP "NEWS,PDH_PDL"
	bot.watchlistSelectorMapMutex.Lock()
	bot.watchlistSelectorMap["SBIN"] = "NEWS,PDH_PDL"
	bot.watchlistSelectorMapMutex.Unlock()
	if bot.HasSymbolIFP("SBIN") {
		t.Errorf("expected HasSymbolIFP(SBIN) to be false without IFP")
	}
}

// TestIFPMultiValidationExecutionGate simulates the execution gate in engine.go:
// verifies that a breakout signal is blocked when require_ifp_validation is true
// and symbol has no IFP confirmation, but allowed once IFP is present.
func TestIFPMultiValidationExecutionGate(t *testing.T) {
	logger, _ := monitoring.NewLogger("info")

	bot := &TradingBot{
		cfg:                  &config.Settings{},
		logger:               logger,
		symbolProvenance:     make(map[string][]string),
		watchlistSelectorMap: make(map[string]string),
		strategyRequireIFPMap: map[string]bool{
			"VANDE_BHARAT": true,
		},
	}

	vbEngine := strategy.NewVandeBharatEngine(logger.Logger, 2.0, 0.5, 1.0, 40.0, 2.0)
	symbol := "TATAMOTORS"

	// Condition 1: Strategy requires IFP, symbol only has "FO"
	bot.symbolProvenanceMutex.Lock()
	bot.symbolProvenance[symbol] = []string{"FO"}
	bot.symbolProvenanceMutex.Unlock()

	shouldGate := bot.IsStrategyRequireIFP(vbEngine.Name()) && !bot.HasSymbolIFP(symbol)
	if !shouldGate {
		t.Fatalf("expected trade to be gated/blocked when symbol lacks IFP")
	}

	// Condition 2: Institutional footprint detected on TATAMOTORS -> "IFP" merged into provenance
	bot.symbolProvenanceMutex.Lock()
	bot.symbolProvenance[symbol] = append(bot.symbolProvenance[symbol], "IFP")
	bot.symbolProvenanceMutex.Unlock()

	shouldGateAfterIFP := bot.IsStrategyRequireIFP(vbEngine.Name()) && !bot.HasSymbolIFP(symbol)
	if shouldGateAfterIFP {
		t.Fatalf("expected trade to be allowed once IFP is confirmed")
	}
	t.Logf("✅ Successfully validated: Breakout signal blocked without IFP, allowed with IFP")
}

// TestLoadModularStrategyConfigsIFP tests parsing of require_ifp_validation
// from TRADING_STRATEGY JSON objects and EQUITY_STRATEGY fallbacks.
func TestLoadModularStrategyConfigsIFP(t *testing.T) {
	logger, _ := monitoring.NewLogger("info")

	bot := &TradingBot{
		cfg:    &config.Settings{},
		logger: logger,
		sysConfigs: map[string]map[string]string{
			"TRADING_STRATEGY": {
				"VANDE_BHARAT": `{"name":"VANDE_BHARAT","enabled":true,"require_ifp_validation":true}`,
				"LOW_VOLUME":   `{"name":"LOW_VOLUME","enabled":true,"require_ifp_validation":false}`,
			},
			"EQUITY_STRATEGY": {
				"fb_require_ifp_validation": "true",
			},
		},
	}

	bot.loadModularStrategyConfigs()

	if !bot.IsStrategyRequireIFP("VANDE_BHARAT") {
		t.Errorf("expected VANDE_BHARAT require_ifp_validation to be true from JSON")
	}
	if bot.IsStrategyRequireIFP("LOW_VOLUME") {
		t.Errorf("expected LOW_VOLUME require_ifp_validation to be false from JSON")
	}
	if !bot.IsStrategyRequireIFP("FAKE_BREAKOUT") {
		t.Errorf("expected FAKE_BREAKOUT require_ifp_validation to be true from EQUITY_STRATEGY fallback")
	}
	if bot.IsStrategyRequireIFP("VANDE_BHARAT_TRAP") {
		t.Errorf("expected VANDE_BHARAT_TRAP require_ifp_validation to be false by default")
	}
}
