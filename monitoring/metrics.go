package monitoring

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus.Registerer"
)

// Metrics holds all Prometheus metrics
type Metrics struct {
	// Order metrics
	OrdersPlaced    prometheus.Counter
	OrdersFilled    prometheus.Counter
	OrdersRejected  prometheus.Counter
	OrdersCancelled prometheus.Counter

	// P&L metrics
	DailyPnL   prometheus.Gauge
	TradeCount prometheus.Gauge
	WinRate    prometheus.Gauge

	// Latency metrics
	APILatency       prometheus.Histogram
	OrderFillLatency prometheus.Histogram

	// Market data metrics
	TicksReceived    prometheus.Counter
	PacketLoss       prometheus.Counter
	CandlesGenerated prometheus.Counter

	// Risk metrics
	AvailableCapital     prometheus.Gauge
	Drawdown             prometheus.Gauge
	CircuitBreakerStatus prometheus.Gauge
}

// NewMetrics creates and registers all metrics
func NewMetrics(reg Registerer) *Metrics {
	m := &Metrics{
		OrdersPlaced: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "trading_orders_placed_total",
			Help: "Total number of orders placed",
		}),
		OrdersFilled: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "trading_orders_filled_total",
			Help: "Total number of orders filled",
		}),
		OrdersRejected: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "trading_orders_rejected_total",
			Help: "Total number of orders rejected",
		}),
		OrdersCancelled: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "trading_orders_cancelled_total",
			Help: "Total number of orders cancelled",
		}),
		DailyPnL: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "trading_daily_pnl",
			Help: "Daily profit and loss",
		}),
		TradeCount: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "trading_trade_count",
			Help: "Number of trades executed today",
		}),
		WinRate: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "trading_win_rate",
			Help: "Win rate percentage",
		}),
		APILatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "trading_api_latency_seconds",
			Help: "API call latency in seconds",
		}),
		OrderFillLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "trading_order_fill_latency_seconds",
			Help: "Order fill latency in seconds",
		}),
		TicksReceived: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "trading_ticks_received_total",
			Help: "Total ticks received",
		}),
		PacketLoss: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "trading_packet_loss_total",
			Help: "Total packet losses detected",
		}),
		CandlesGenerated: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "trading_candles_generated_total",
			Help: "Total OHLCV candles generated",
		}),
		AvailableCapital: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "trading_available_capital",
			Help: "Available trading capital",
		}),
		Drawdown: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "trading_drawdown_percent",
			Help: "Current drawdown percentage",
		}),
		CircuitBreakerStatus: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "trading_circuit_breaker_active",
			Help: "Circuit breaker status (1=active, 0=inactive)",
		}),
	}

	// Register all metrics
	reg.MustRegister(
		m.OrdersPlaced, m.OrdersFilled, m.OrdersRejected, m.OrdersCancelled,
		m.DailyPnL, m.TradeCount, m.WinRate,
		m.APILatency, m.OrderFillLatency,
		m.TicksReceived, m.PacketLoss, m.CandlesGenerated,
		m.AvailableCapital, m.Drawdown, m.CircuitBreakerStatus,
	)

	return m
}
