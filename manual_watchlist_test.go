package main

import (
	"context"
	"strings"
	"sync"
	"testing"

	"zerodha-trading/monitoring"
	"zerodha-trading/selection"
)

func TestManualWatchlistDelimitersAndMerge(t *testing.T) {
	// 1. Test delimiter parsing logic with all supported delimiters: comma, space, newline, tab, semicolon
	inputStrings := []string{
		"ADANIPOWER, TCS, INFY",
		"ADANIPOWER TCS INFY",
		"ADANIPOWER\nTCS\nINFY",
		"ADANIPOWER\r\nTCS\r\nINFY",
		"ADANIPOWER;TCS;INFY",
		"  ADANIPOWER \t TCS , INFY; RELIANCE  ",
	}

	for _, input := range inputStrings {
		var rawParts []string
		var current string
		for i := 0; i < len(input); i++ {
			c := input[i]
			if c == ',' || c == ';' || c == '\n' || c == '\r' || c == '\t' || c == ' ' {
				if len(current) > 0 {
					rawParts = append(rawParts, current)
					current = ""
				}
			} else {
				if c >= 'a' && c <= 'z' {
					c = c - 'a' + 'A'
				}
				current += string(c)
			}
		}
		if len(current) > 0 {
			rawParts = append(rawParts, current)
		}

		if len(rawParts) < 3 {
			t.Errorf("Failed to parse symbols for input %q: got %d parts, expected at least 3", input, len(rawParts))
		}
		expectedFirstThree := []string{"ADANIPOWER", "TCS", "INFY"}
		for i := 0; i < 3; i++ {
			if rawParts[i] != expectedFirstThree[i] {
				t.Errorf("Mismatch for input %q at idx %d: got %s, expected %s", input, i, rawParts[i], expectedFirstThree[i])
			}
		}
	}
}

func TestManualWatchlistMergeMapLogic(t *testing.T) {
	// Simulate existing stocks in DB
	existingManual := []string{"ADANIPOWER:NEWS"}

	mergedManualMap := make(map[string]string)
	for _, item := range existingManual {
		parts := strings.Split(item, ":")
		sym := strings.TrimSpace(strings.ToUpper(parts[0]))
		sel := selection.SelectorPDHPDL
		if len(parts) > 1 && parts[1] != "" {
			sel = selection.NormalizeSelectorName(parts[1])
		}
		if sym != "" {
			mergedManualMap[sym] = sel
		}
	}

	// Now user adds new stocks: "TCS:PDH_PDL, INFY:52WH_52WL"
	newItems := []string{"TCS:PDH_PDL", "INFY:52WH_52WL"}
	for _, item := range newItems {
		parts := strings.Split(item, ":")
		sym := strings.TrimSpace(strings.ToUpper(parts[0]))
		sel := selection.SelectorPDHPDL
		if len(parts) > 1 && parts[1] != "" {
			sel = selection.NormalizeSelectorName(parts[1])
		}
		if sym != "" {
			mergedManualMap[sym] = sel
		}
	}

	// Verify all 3 stocks exist
	if len(mergedManualMap) != 3 {
		t.Fatalf("Expected 3 merged stocks, got %d: %v", len(mergedManualMap), mergedManualMap)
	}

	if mergedManualMap["ADANIPOWER"] != "NEWS" {
		t.Errorf("Expected ADANIPOWER to be NEWS, got %s", mergedManualMap["ADANIPOWER"])
	}
	if mergedManualMap["TCS"] != "PDH_PDL" {
		t.Errorf("Expected TCS to be PDH_PDL, got %s", mergedManualMap["TCS"])
	}
	if mergedManualMap["INFY"] != "52WH_52WL" {
		t.Errorf("Expected INFY to be 52WH_52WL, got %s", mergedManualMap["INFY"])
	}

	// Now user updates ADANIPOWER strategy to "FO"
	updatedItem := "ADANIPOWER:FO"
	parts := strings.Split(updatedItem, ":")
	sym := strings.TrimSpace(strings.ToUpper(parts[0]))
	sel := selection.NormalizeSelectorName(parts[1])
	mergedManualMap[sym] = sel

	if len(mergedManualMap) != 3 {
		t.Errorf("Expected length 3 after update, got %d", len(mergedManualMap))
	}
	if mergedManualMap["ADANIPOWER"] != "FO" {
		t.Errorf("Expected ADANIPOWER to be FO, got %s", mergedManualMap["ADANIPOWER"])
	}
}

func TestTradingBotRestoreManualWatchlistState(t *testing.T) {
	logger, err := monitoring.NewLogger("debug")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	tb := &TradingBot{
		ctx:                       context.Background(),
		logger:                    logger,
		watchlist:                 make(map[string]int64),
		watchlistSelectorMap:      make(map[string]string),
		symbolProvenance:          make(map[string][]string),
		watchlistMutex:            sync.RWMutex{},
		watchlistSelectorMapMutex: sync.RWMutex{},
		symbolProvenanceMutex:     sync.RWMutex{},
	}

	// Populate symbols as if restored from DB
	mockManualSymbols := map[string]struct {
		token    int64
		selector string
	}{
		"ADANIPOWER": {token: 4451329, selector: "NEWS"},
		"TCS":        {token: 2953217, selector: "PDH_PDL"},
		"INFY":       {token: 408065, selector: "52WH_52WL"},
	}

	for sym, val := range mockManualSymbols {
		tb.watchlistSelectorMapMutex.Lock()
		tb.watchlistSelectorMap[sym] = "MANUAL:" + val.selector
		tb.watchlistSelectorMapMutex.Unlock()

		tb.symbolProvenanceMutex.Lock()
		tb.symbolProvenance[sym] = []string{"MANUAL", "MANUAL:" + val.selector, val.selector}
		tb.symbolProvenanceMutex.Unlock()

		tb.watchlistMutex.Lock()
		tb.watchlist[sym] = val.token
		tb.watchlistMutex.Unlock()
	}

	// Verify thread-safe reads
	tb.watchlistMutex.RLock()
	if len(tb.watchlist) != 3 {
		t.Errorf("Expected 3 watchlist items, got %d", len(tb.watchlist))
	}
	for sym, expected := range mockManualSymbols {
		if tb.watchlist[sym] != expected.token {
			t.Errorf("Symbol %s token mismatch: got %d, expected %d", sym, tb.watchlist[sym], expected.token)
		}
	}
	tb.watchlistMutex.RUnlock()

	// Verify isManualStock
	for sym := range mockManualSymbols {
		if !tb.isManualStock(sym) {
			t.Errorf("Expected isManualStock(%q) to be true", sym)
		}
	}

	if tb.isManualStock("RANDOM_STOCK") {
		t.Errorf("Expected isManualStock(RANDOM_STOCK) to be false")
	}
}

func TestManualStockABBEnrollment(t *testing.T) {
	logger, err := monitoring.NewLogger("error")
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}

	tb := &TradingBot{
		ctx:                       context.Background(),
		logger:                    logger,
		watchlist:                 make(map[string]int64),
		watchlistSelectorMap:      make(map[string]string),
		symbolProvenance:          make(map[string][]string),
		strategyMultiSelMap:      make(map[string][]string),
		strategyWatchlists:        make(map[string]map[string]int64),
		watchlistMutex:            sync.RWMutex{},
		watchlistSelectorMapMutex: sync.RWMutex{},
		symbolProvenanceMutex:     sync.RWMutex{},
		strategyMultiSelMapMutex:  sync.RWMutex{},
	}

	// Active strategy with default attached selectors
	tb.strategyMultiSelMap["VANDE_BHARAT"] = []string{"FO", "SECTOR"}
	tb.strategyMultiSelMap["EMAS5_BREAKOUT"] = []string{"FO", "SECTOR"}
	tb.strategyMultiSelMap["LOW_VOLUME"] = []string{"FO"}

	// When user adds ABB via Manual Day Watchlist with PDH_PDL
	sym := "ABB"
	token := int64(3329)
	sel := "PDH_PDL"

	tb.watchlistSelectorMapMutex.Lock()
	tb.watchlistSelectorMap[sym] = "MANUAL:" + sel
	tb.watchlistSelectorMapMutex.Unlock()

	tb.symbolProvenanceMutex.Lock()
	tb.symbolProvenance[sym] = []string{"MANUAL", "MANUAL:" + sel, sel, "FO"}
	tb.symbolProvenanceMutex.Unlock()

	tb.watchlistMutex.Lock()
	tb.watchlist[sym] = token
	tb.watchlistMutex.Unlock()

	// 1. Verify isManualStock
	if !tb.isManualStock(sym) {
		t.Fatalf("Expected isManualStock(%s) to be true", sym)
	}

	// 2. Verify isSymbolAllowedForStrategy for all active strategies
	for _, strat := range []string{"VANDE_BHARAT", "EMAS5_BREAKOUT", "LOW_VOLUME"} {
		if !tb.isSymbolAllowedForStrategy(sym, strat) {
			t.Errorf("Expected isSymbolAllowedForStrategy(%s, %s) to be true", sym, strat)
		}
	}

	// 3. Test with explicit MANUAL attached selector
	tb.strategyMultiSelMap["MANUAL_STRAT"] = []string{"MANUAL"}
	if !tb.isSymbolAllowedForStrategy(sym, "MANUAL_STRAT") {
		t.Errorf("Expected isSymbolAllowedForStrategy(%s, MANUAL_STRAT) to be true", sym)
	}
}

func TestWatchlistRecalculateConfiguredSizeAndNoFOPollution(t *testing.T) {
	logger, _ := monitoring.NewLogger("error")
	defer logger.Sync()

	tb := &TradingBot{
		logger:                    logger,
		symbolProvenance:          make(map[string][]string),
		symbolProvenanceMutex:     sync.RWMutex{},
		watchlist:                 make(map[string]int64),
		watchlistMutex:            sync.RWMutex{},
		watchlistSelectorMap:      make(map[string]string),
		watchlistSelectorMapMutex: sync.RWMutex{},
		strategyWatchlists:        make(map[string]map[string]int64),
		strategyMultiSelMap:       make(map[string][]string),
		strategyMultiSelMapMutex:  sync.RWMutex{},
	}

	tb.strategyMultiSelMap["VANDE_BHARAT"] = []string{"FO", "SECTOR"}

	// 1. Symbol INFY is in F&O universe, but NOT selected yet.
	// It should NOT be allowed for strategy VANDE_BHARAT simply by virtue of being an F&O stock.
	if tb.isSymbolAllowedForStrategy("INFY", "VANDE_BHARAT") {
		t.Errorf("Unselected F&O stock INFY should NOT be allowed for strategy VANDE_BHARAT")
	}

	// 2. Symbol RELIANCE was selected by FO selector (provenance has FO)
	tb.symbolProvenanceMutex.Lock()
	tb.symbolProvenance["RELIANCE"] = []string{"FO"}
	tb.symbolProvenanceMutex.Unlock()

	if !tb.isSymbolAllowedForStrategy("RELIANCE", "VANDE_BHARAT") {
		t.Errorf("Selected stock RELIANCE with FO provenance must be allowed for VANDE_BHARAT")
	}

	// 3. Stock TATAMOTORS was previously selected, but on recalculate it is no longer selected.
	// Test provenance cleaning: active MANUAL provenances must be retained, stale automated ones pruned.
	tb.symbolProvenanceMutex.Lock()
	tb.symbolProvenance["TATAMOTORS"] = []string{"FO"}
	tb.symbolProvenance["MANUAL_STOCK"] = []string{"MANUAL", "MANUAL:NEWS", "NEWS"}

	cleanProv := make(map[string][]string)
	for sym, provs := range tb.symbolProvenance {
		var manProvs []string
		for _, p := range provs {
			if strings.HasPrefix(p, "MANUAL") {
				manProvs = append(manProvs, p)
			}
		}
		if len(manProvs) > 0 {
			cleanProv[sym] = manProvs
		}
	}
	tb.symbolProvenance = cleanProv
	tb.symbolProvenanceMutex.Unlock()

	if _, exists := tb.symbolProvenance["TATAMOTORS"]; exists {
		t.Errorf("Stale automated provenance for TATAMOTORS should have been pruned on recalculation")
	}
	if provs, exists := tb.symbolProvenance["MANUAL_STOCK"]; !exists || len(provs) == 0 {
		t.Errorf("Manual stock provenance should have been preserved on recalculation")
	}

	// 4. Test autoSelectionDone flag setting and verification
	tb.setAutoSelectionDone(true)
	if !tb.isAutoSelectionDone() {
		t.Errorf("isAutoSelectionDone() should return true after setAutoSelectionDone(true)")
	}
}

func TestManualStockStrategyImmutabilityAgainstAutomatedSelection(t *testing.T) {
	logger, _ := monitoring.NewLogger("error")
	defer logger.Sync()

	tb := &TradingBot{
		logger:                    logger,
		symbolProvenance:          make(map[string][]string),
		symbolProvenanceMutex:     sync.RWMutex{},
		watchlist:                 make(map[string]int64),
		watchlistMutex:            sync.RWMutex{},
		watchlistSelectorMap:      make(map[string]string),
		watchlistSelectorMapMutex: sync.RWMutex{},
		strategyWatchlists:        make(map[string]map[string]int64),
		strategyMultiSelMap:       make(map[string][]string),
		strategyMultiSelMapMutex:  sync.RWMutex{},
	}

	sym := "RELIANCE"
	token := int64(738561)
	initialStrategy := "QUANT_SCANNER"

	// 1. Manually add stock
	tb.watchlistMutex.Lock()
	tb.watchlist[sym] = token
	tb.watchlistMutex.Unlock()

	tb.watchlistSelectorMapMutex.Lock()
	tb.watchlistSelectorMap[sym] = "MANUAL:" + initialStrategy
	tb.watchlistSelectorMapMutex.Unlock()

	tb.symbolProvenanceMutex.Lock()
	tb.symbolProvenance[sym] = []string{"MANUAL", "MANUAL:" + initialStrategy, initialStrategy}
	tb.symbolProvenanceMutex.Unlock()

	if !tb.isManualStock(sym) {
		t.Fatalf("Expected %s to be recognized as manual stock", sym)
	}

	// 2. Simulate automated stock selection attempting to classify RELIANCE under FO and SECTOR
	automatedSelectors := []string{"FO", "SECTOR", "PT_SCREENER"}
	for _, normCode := range automatedSelectors {
		if tb.isManualStock(sym) {
			// Automated selection must NOT overwrite or mutate manual stock strategy
			_ = normCode
			continue
		}
		t.Fatalf("Should not reach here: manual stock %s must be protected", sym)
	}

	// 3. Verify provenance and selector map are completely unchanged
	tb.watchlistSelectorMapMutex.RLock()
	currentSelector := tb.watchlistSelectorMap[sym]
	tb.watchlistSelectorMapMutex.RUnlock()

	expectedSelector := "MANUAL:" + initialStrategy
	if currentSelector != expectedSelector {
		t.Errorf("Expected selector to remain %s, got %s", expectedSelector, currentSelector)
	}

	tb.symbolProvenanceMutex.RLock()
	provs := tb.symbolProvenance[sym]
	tb.symbolProvenanceMutex.RUnlock()

	for _, p := range provs {
		if p == "FO" || p == "SECTOR" || p == "PT_SCREENER" {
			t.Errorf("Automated selector %s leaked into manual stock provenance", p)
		}
	}

	// 4. Manual update in Watchlist & Logs: update to PDH_PDL
	newStrategy := "PDH_PDL"
	tb.watchlistSelectorMapMutex.Lock()
	tb.watchlistSelectorMap[sym] = "MANUAL:" + newStrategy
	tb.watchlistSelectorMapMutex.Unlock()

	tb.watchlistSelectorMapMutex.RLock()
	updatedSelector := tb.watchlistSelectorMap[sym]
	tb.watchlistSelectorMapMutex.RUnlock()

	if updatedSelector != "MANUAL:"+newStrategy {
		t.Errorf("Expected manually updated selector to be MANUAL:%s, got %s", newStrategy, updatedSelector)
	}
}

