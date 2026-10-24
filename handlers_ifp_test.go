package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"zerodha-trading/config"
	"zerodha-trading/data"
	"zerodha-trading/monitoring"
	"zerodha-trading/scanner"
)

func TestIFPHandlers(t *testing.T) {
	logger, _ := monitoring.NewLogger("info")
	bot := &TradingBot{
		cfg:                &config.Settings{},
		logger:             logger,
		volumeSurgeScanner: scanner.NewVolumeSurgeScanner(nil, logger.Logger),
		foAnalyzer:         scanner.NewFOAnalyzer(nil, logger.Logger),
		watchlist:          make(map[string]int64),
		sysConfigs:         make(map[string]map[string]string),
	}

	// 1. Test handleIFPVolumeSurge
	t.Run("handleIFPVolumeSurge", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/ifp/volume-surge?lookback=20&multiplier=2.0", nil)
		rr := httptest.NewRecorder()
		bot.handleIFPVolumeSurge(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rr.Code)
		}

		var resp map[string]interface{}
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode json response: %v", err)
		}
		if resp["lookback_days"].(float64) != 20 {
			t.Errorf("expected lookback_days 20, got %v", resp["lookback_days"])
		}
		if resp["min_multiplier"].(float64) != 2.0 {
			t.Errorf("expected min_multiplier 2.0, got %v", resp["min_multiplier"])
		}
	})

	// 2. Test handleIFPFOMatrix
	t.Run("handleIFPFOMatrix", func(t *testing.T) {
		// Seed a summary in foAnalyzer
		strikes := []data.FOStrikeRow{
			{StrikePrice: 1400, CallOI: 200000, PutOI: 800000, PutOIChange: 200000, PutLTP: 12.5},
			{StrikePrice: 1420, CallOI: 450000, PutOI: 950000, PutOIChange: 350000, PutLTP: 22.0},
			{StrikePrice: 1440, CallOI: 900000, PutOI: 300000, PutOIChange: 40000, CallLTP: 16.0},
		}
		bot.foAnalyzer.BuildFOSummary("RELIANCE", 1422.0, 1420.0, "2026-10-29", scanner.BuildupLongBuildup, strikes, time.Now())

		req := httptest.NewRequest("GET", "/api/ifp/fo-matrix?symbol=RELIANCE", nil)
		rr := httptest.NewRecorder()
		bot.handleIFPFOMatrix(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rr.Code)
		}

		var summary data.FOSummaryScore
		if err := json.Unmarshal(rr.Body.Bytes(), &summary); err != nil {
			t.Fatalf("failed to decode fo matrix response: %v", err)
		}
		if summary.Symbol != "RELIANCE" {
			t.Errorf("expected symbol RELIANCE, got %s", summary.Symbol)
		}
		if summary.InstitutionalScore <= 0 {
			t.Errorf("expected positive institutional score, got %.1f", summary.InstitutionalScore)
		}
	})

	// 3. Test handleIFPFOAll
	t.Run("handleIFPFOAll", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/ifp/fo-all", nil)
		rr := httptest.NewRecorder()
		bot.handleIFPFOAll(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rr.Code)
		}

		var resp map[string]interface{}
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode fo-all response: %v", err)
		}
		if resp["count"].(float64) < 1 {
			t.Errorf("expected at least 1 summary, got %v", resp["count"])
		}
	})

	// 4. Test handleIFPFuturesBuildup
	t.Run("handleIFPFuturesBuildup", func(t *testing.T) {
		bot.foAnalyzer.SetFuturesBuildups([]data.FuturesBuildupRecord{
			{Symbol: "RELIANCE", SpotPrice: 1422.0, FuturesPrice: 1428.5, BuildupType: scanner.BuildupLongBuildup},
		})

		req := httptest.NewRequest("GET", "/api/ifp/futures-buildup", nil)
		rr := httptest.NewRecorder()
		bot.handleIFPFuturesBuildup(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", rr.Code)
		}

		var resp map[string]interface{}
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode futures-buildup response: %v", err)
		}
		if resp["count"].(float64) != 1 {
			t.Errorf("expected count 1, got %v", resp["count"])
		}
	})

	// 5. Test handleIFPSettings (POST & GET)
	t.Run("handleIFPSettings", func(t *testing.T) {
		// POST update
		payload := map[string]string{
			"volume_lookback_days": "30",
			"volume_surge_multiplier": "3.0",
		}
		bodyBytes, _ := json.Marshal(payload)
		reqPost := httptest.NewRequest("POST", "/api/ifp/settings", bytes.NewBuffer(bodyBytes))
		rrPost := httptest.NewRecorder()
		bot.handleIFPSettings(rrPost, reqPost)

		if rrPost.Code != http.StatusOK {
			t.Fatalf("expected POST status 200, got %d", rrPost.Code)
		}

		// GET read
		reqGet := httptest.NewRequest("GET", "/api/ifp/settings", nil)
		rrGet := httptest.NewRecorder()
		bot.handleIFPSettings(rrGet, reqGet)

		if rrGet.Code != http.StatusOK {
			t.Fatalf("expected GET status 200, got %d", rrGet.Code)
		}

		var resp map[string]interface{}
		if err := json.Unmarshal(rrGet.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode settings response: %v", err)
		}
		settings := resp["settings"].(map[string]interface{})
		if settings["volume_lookback_days"] != "30" {
			t.Errorf("expected volume_lookback_days 30, got %v", settings["volume_lookback_days"])
		}
		if settings["volume_surge_multiplier"] != "3.0" {
			t.Errorf("expected volume_surge_multiplier 3.0, got %v", settings["volume_surge_multiplier"])
		}
	})
}
