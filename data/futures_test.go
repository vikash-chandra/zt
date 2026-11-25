package data

import (
	"context"
	"testing"

	"go.uber.org/zap"
)

func TestGetNearestFuturesContracts(t *testing.T) {
	sm := NewSecurityMaster(nil, nil, zap.NewNop())
	now := NowIST()
	yesterday := now.AddDate(0, 0, -1)
	nearExp := now.AddDate(0, 0, 5)
	farExp := now.AddDate(0, 0, 35)

	sm.InjectOptionInstruments("NFO", Instruments{
		{
			InstrumentToken: 1001,
			TradingSymbol:   "NIFTYEXPIRED",
			Name:            "NIFTY",
			InstrumentType:  "FUT",
			Expiry:          yesterday,
		},
		{
			InstrumentToken: 1002,
			TradingSymbol:   "NIFTYFAR",
			Name:            "NIFTY",
			InstrumentType:  "FUT",
			Expiry:          farExp,
		},
		{
			InstrumentToken: 1003,
			TradingSymbol:   "NIFTYNEAR",
			Name:            "NIFTY",
			InstrumentType:  "FUT",
			Expiry:          nearExp,
		},
		{
			InstrumentToken: 1004,
			TradingSymbol:   "NIFTY25000CE",
			Name:            "NIFTY",
			InstrumentType:  "CE",
			Strike:          25000,
			Expiry:          nearExp,
		},
		{
			InstrumentToken: 1005,
			TradingSymbol:   "RELIANCEFUT",
			Name:            "RELIANCE",
			InstrumentType:  "FUT",
			Expiry:          nearExp,
		},
	})

	nearest, err := sm.GetNearestFuturesContracts(context.Background(), "NFO")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got, ok := nearest["NIFTY"]; !ok || got.TradingSymbol != "NIFTYNEAR" {
		t.Fatalf("expected NIFTYNEAR as nearest NIFTY futures contract, got %+v (ok=%v)", got, ok)
	}
	if got, ok := nearest["RELIANCE"]; !ok || got.TradingSymbol != "RELIANCEFUT" {
		t.Fatalf("expected RELIANCEFUT for RELIANCE, got %+v (ok=%v)", got, ok)
	}
	if _, hasOpt := nearest["NIFTY25000CE"]; hasOpt {
		t.Fatalf("did not expect option contract in GetNearestFuturesContracts result")
	}
}
