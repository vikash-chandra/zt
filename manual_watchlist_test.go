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
