package data

import "time"

// OrderParams defines parameters for placing/modifying an order
type OrderParams struct {
	Exchange          string
	TradingSymbol     string
	TransactionType   string
	Quantity          int
	Price             float64
	TriggerPrice      float64
	OrderType         string
	Product           string
	Validity          string
	DisclosedQuantity int
	Tag               string
}

// OrderResponse represents the response received from the broker after placing/canceling an order
type OrderResponse struct {
	OrderID string
}

// Order represents an order status or history entry
type Order struct {
	OrderID           string
	ParentOrderID     string
	Exchange          string
	TradingSymbol     string
	TransactionType   string // "BUY", "SELL"
	OrderType         string // "MARKET", "LIMIT", "SL", "SL-M"
	Product           string // "MIS", "CNC", etc.
	Price             float64
	TriggerPrice      float64
	Quantity          int
	Status            string // "COMPLETE", "REJECTED", "CANCELLED", etc.
	AveragePrice      float64
	FilledQuantity    int
	PendingQuantity   int
	CancelledQuantity int
	OrderTimestamp    time.Time
	StatusMessage     string
	Variety           string
}

// Position represents an active trade position
type Position struct {
	TradingSymbol string
	Exchange      string
	Product       string
	Quantity      int
	AveragePrice  float64
	ClosePrice    float64
	Value         float64
	Pnl           float64
	M2M           float64
	BuyQuantity   int
	BuyPrice      float64
	SellQuantity  int
	SellPrice     float64
}

// Positions contains lists of net and day positions
type Positions struct {
	Net []Position
	Day []Position
}

// HistoricalData represents a single candle item
type HistoricalData struct {
	Date   time.Time
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume int64
}

// Instrument represents a security contract or symbol
type Instrument struct {
	InstrumentToken uint32
	ExchangeToken   uint32
	TradingSymbol   string
	Name            string
	LastPrice       float64
	Expiry          time.Time
	Strike          float64
	TickSize        float64
	LotSize         int
	InstrumentType  string
	Segment         string
	Exchange        string
}

// Instruments represents a list of Instrument
type Instruments []Instrument

// DepthItem represents bid/ask depth entry
type DepthItem struct {
	Price    float64
	Quantity int
}

// Depth represents buy/sell order book depth
type Depth struct {
	Buy  []DepthItem
	Sell []DepthItem
}

// Quote represents real-time price and depth snapshots
type Quote struct {
	InstrumentToken uint32
	LastPrice       float64
	VolumeTraded    int64
	OHLC            OHLC
	Depth           Depth
}

// OHLC represents Open, High, Low, Close for Quotes
type OHLC struct {
	Open  float64
	High  float64
	Low   float64
	Close float64
}

// OHLCQuote represents dynamic EOD/current quote bounds
type OHLCQuote struct {
	InstrumentToken uint32
	LastPrice       float64
	OHLC            OHLC
}

// QuoteOHLC represents map of instruments to OHLCQuote
type QuoteOHLC map[string]OHLCQuote

// OrderMargins represents the margins for an order
type OrderMargins struct {
	TradingSymbol string
	Total         float64
	Span          float64
	Exposure      float64
	Cash          float64
}
