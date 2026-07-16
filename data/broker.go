package data

import (
	"time"

	kiteconnect "github.com/zerodha/gokiteconnect/v4"
)

// BrokerClient represents the interface for broker API interactions.
type BrokerClient interface {
	SetAccessToken(token string)
	GetPositions() (Positions, error)
	GetOrders() ([]Order, error)
	PlaceOrder(variety string, params OrderParams) (OrderResponse, error)
	CancelOrder(variety string, orderID string, parentOrderID *string) (OrderResponse, error)
	GetOrderHistory(orderID string) ([]Order, error)
	GetHistoricalData(instrumentToken int, interval string, fromTime time.Time, toTime time.Time, continuous bool, oi bool) ([]HistoricalData, error)
	GetInstrumentsByExchange(exchange string) (Instruments, error)
	GetOHLC(keys ...string) (QuoteOHLC, error)
	GetOrderMargins(params []OrderParams) ([]OrderMargins, error)
	ModifyOrder(variety string, orderID string, params OrderParams) (OrderResponse, error)
	GetQuote(instruments ...string) (map[string]Quote, error)
	GenerateSession(requestToken string, apiSecret string) (string, error)
}

// ZerodhaBrokerAdapter implements the BrokerClient interface wrapping the real Zerodha Kite connect client
type ZerodhaBrokerAdapter struct {
	Client *kiteconnect.Client
}

// NewZerodhaBrokerAdapter creates a new adapter instance
func NewZerodhaBrokerAdapter(client *kiteconnect.Client) *ZerodhaBrokerAdapter {
	return &ZerodhaBrokerAdapter{Client: client}
}

func (a *ZerodhaBrokerAdapter) SetAccessToken(token string) {
	a.Client.SetAccessToken(token)
}

func (a *ZerodhaBrokerAdapter) GetPositions() (Positions, error) {
	kPos, err := a.Client.GetPositions()
	if err != nil {
		return Positions{}, err
	}

	var pos Positions
	pos.Net = make([]Position, len(kPos.Net))
	for i, p := range kPos.Net {
		pos.Net[i] = Position{
			TradingSymbol: p.Tradingsymbol,
			Exchange:      p.Exchange,
			Product:       p.Product,
			Quantity:      p.Quantity,
			AveragePrice:  p.AveragePrice,
			ClosePrice:    p.ClosePrice,
			Value:         p.Value,
			Pnl:           p.PnL,
			M2M:           p.M2M,
			BuyQuantity:   p.BuyQuantity,
			BuyPrice:      p.BuyPrice,
			SellQuantity:  p.SellQuantity,
			SellPrice:     p.SellPrice,
		}
	}
	pos.Day = make([]Position, len(kPos.Day))
	for i, p := range kPos.Day {
		pos.Day[i] = Position{
			TradingSymbol: p.Tradingsymbol,
			Exchange:      p.Exchange,
			Product:       p.Product,
			Quantity:      p.Quantity,
			AveragePrice:  p.AveragePrice,
			ClosePrice:    p.ClosePrice,
			Value:         p.Value,
			Pnl:           p.PnL,
			M2M:           p.M2M,
			BuyQuantity:   p.BuyQuantity,
			BuyPrice:      p.BuyPrice,
			SellQuantity:  p.SellQuantity,
			SellPrice:     p.SellPrice,
		}
	}
	return pos, nil
}

func mapKiteOrder(o kiteconnect.Order) Order {
	return Order{
		OrderID:           o.OrderID,
		ParentOrderID:     o.ParentOrderID,
		Exchange:          o.Exchange,
		TradingSymbol:     o.TradingSymbol,
		TransactionType:   o.TransactionType,
		OrderType:         o.OrderType,
		Product:           o.Product,
		Price:             o.Price,
		TriggerPrice:      o.TriggerPrice,
		Quantity:          int(o.Quantity),
		Status:            o.Status,
		AveragePrice:      o.AveragePrice,
		FilledQuantity:    int(o.FilledQuantity),
		PendingQuantity:   int(o.PendingQuantity),
		CancelledQuantity: int(o.CancelledQuantity),
		OrderTimestamp:    o.OrderTimestamp.Time,
		StatusMessage:     o.StatusMessage,
		Variety:           o.Variety,
	}
}

func (a *ZerodhaBrokerAdapter) GetOrders() ([]Order, error) {
	kOrders, err := a.Client.GetOrders()
	if err != nil {
		return nil, err
	}
	orders := make([]Order, len(kOrders))
	for i, o := range kOrders {
		orders[i] = mapKiteOrder(o)
	}
	return orders, nil
}

func mapOrderParams(p OrderParams) kiteconnect.OrderParams {
	return kiteconnect.OrderParams{
		Exchange:          p.Exchange,
		Tradingsymbol:     p.TradingSymbol,
		TransactionType:   p.TransactionType,
		Quantity:          p.Quantity,
		Price:             p.Price,
		TriggerPrice:      p.TriggerPrice,
		OrderType:         p.OrderType,
		Product:           p.Product,
		Validity:          p.Validity,
		DisclosedQuantity: p.DisclosedQuantity,
		Tag:               p.Tag,
	}
}

func (a *ZerodhaBrokerAdapter) PlaceOrder(variety string, params OrderParams) (OrderResponse, error) {
	resp, err := a.Client.PlaceOrder(variety, mapOrderParams(params))
	if err != nil {
		return OrderResponse{}, err
	}
	return OrderResponse{OrderID: resp.OrderID}, nil
}

func (a *ZerodhaBrokerAdapter) CancelOrder(variety string, orderID string, parentOrderID *string) (OrderResponse, error) {
	resp, err := a.Client.CancelOrder(variety, orderID, parentOrderID)
	if err != nil {
		return OrderResponse{}, err
	}
	return OrderResponse{OrderID: resp.OrderID}, nil
}

func (a *ZerodhaBrokerAdapter) GetOrderHistory(orderID string) ([]Order, error) {
	kOrders, err := a.Client.GetOrderHistory(orderID)
	if err != nil {
		return nil, err
	}
	orders := make([]Order, len(kOrders))
	for i, o := range kOrders {
		orders[i] = mapKiteOrder(o)
	}
	return orders, nil
}

func (a *ZerodhaBrokerAdapter) GetHistoricalData(instrumentToken int, interval string, fromTime time.Time, toTime time.Time, continuous bool, oi bool) ([]HistoricalData, error) {
	kData, err := a.Client.GetHistoricalData(instrumentToken, interval, fromTime, toTime, continuous, oi)
	if err != nil {
		return nil, err
	}
	data := make([]HistoricalData, len(kData))
	for i, d := range kData {
		data[i] = HistoricalData{
			Date:   d.Date.Time,
			Open:   d.Open,
			High:   d.High,
			Low:    d.Low,
			Close:  d.Close,
			Volume: int64(d.Volume),
		}
	}
	return data, nil
}

func (a *ZerodhaBrokerAdapter) GetInstrumentsByExchange(exchange string) (Instruments, error) {
	kInstr, err := a.Client.GetInstrumentsByExchange(exchange)
	if err != nil {
		return nil, err
	}
	instr := make(Instruments, len(kInstr))
	for i, d := range kInstr {
		instr[i] = Instrument{
			InstrumentToken: uint32(d.InstrumentToken),
			ExchangeToken:   uint32(d.ExchangeToken),
			TradingSymbol:   d.Tradingsymbol,
			Name:            d.Name,
			LastPrice:       d.LastPrice,
			Expiry:          d.Expiry.Time,
			Strike:          d.StrikePrice,
			TickSize:        d.TickSize,
			LotSize:         int(d.LotSize),
			InstrumentType:  d.InstrumentType,
			Segment:         d.Segment,
			Exchange:        d.Exchange,
		}
	}
	return instr, nil
}

func (a *ZerodhaBrokerAdapter) GetOHLC(keys ...string) (QuoteOHLC, error) {
	kOHLC, err := a.Client.GetOHLC(keys...)
	if err != nil {
		return nil, err
	}
	res := make(QuoteOHLC)
	for k, v := range kOHLC {
		res[k] = OHLCQuote{
			InstrumentToken: uint32(v.InstrumentToken),
			LastPrice:       v.LastPrice,
			OHLC: OHLC{
				Open:  v.OHLC.Open,
				High:  v.OHLC.High,
				Low:   v.OHLC.Low,
				Close: v.OHLC.Close,
			},
		}
	}
	return res, nil
}

func (a *ZerodhaBrokerAdapter) GetOrderMargins(params []OrderParams) ([]OrderMargins, error) {
	kParams := make([]kiteconnect.OrderMarginParam, len(params))
	for i, p := range params {
		kParams[i] = kiteconnect.OrderMarginParam{
			Exchange:        p.Exchange,
			Tradingsymbol:   p.TradingSymbol,
			TransactionType: p.TransactionType,
			Variety:         "regular",
			Product:         p.Product,
			OrderType:       p.OrderType,
			Quantity:        float64(p.Quantity),
			Price:           p.Price,
			TriggerPrice:    p.TriggerPrice,
		}
	}
	kMargins, err := a.Client.GetOrderMargins(kiteconnect.GetMarginParams{
		OrderParams: kParams,
	})
	if err != nil {
		return nil, err
	}
	margins := make([]OrderMargins, len(kMargins))
	for i, m := range kMargins {
		margins[i] = OrderMargins{
			TradingSymbol: m.TradingSymbol,
			Total:         m.Total,
			Span:          m.SPAN,
			Exposure:      m.Exposure,
			Cash:          m.Cash,
		}
	}
	return margins, nil
}

func (a *ZerodhaBrokerAdapter) ModifyOrder(variety string, orderID string, params OrderParams) (OrderResponse, error) {
	resp, err := a.Client.ModifyOrder(variety, orderID, mapOrderParams(params))
	if err != nil {
		return OrderResponse{}, err
	}
	return OrderResponse{OrderID: resp.OrderID}, nil
}

func (a *ZerodhaBrokerAdapter) GetQuote(instruments ...string) (map[string]Quote, error) {
	kQuotes, err := a.Client.GetQuote(instruments...)
	if err != nil {
		return nil, err
	}
	res := make(map[string]Quote)
	for k, v := range kQuotes {
		var buy, sell []DepthItem
		for _, b := range v.Depth.Buy {
			buy = append(buy, DepthItem{Price: b.Price, Quantity: int(b.Quantity)})
		}
		for _, s := range v.Depth.Sell {
			sell = append(sell, DepthItem{Price: s.Price, Quantity: int(s.Quantity)})
		}
		res[k] = Quote{
			InstrumentToken: uint32(v.InstrumentToken),
			LastPrice:       v.LastPrice,
			VolumeTraded:    int64(v.Volume),
			OHLC: OHLC{
				Open:  v.OHLC.Open,
				High:  v.OHLC.High,
				Low:   v.OHLC.Low,
				Close: v.OHLC.Close,
			},
			Depth: Depth{
				Buy:  buy,
				Sell: sell,
			},
		}
	}
	return res, nil
}

func (a *ZerodhaBrokerAdapter) GenerateSession(requestToken string, apiSecret string) (string, error) {
	session, err := a.Client.GenerateSession(requestToken, apiSecret)
	if err != nil {
		return "", err
	}
	return session.AccessToken, nil
}
