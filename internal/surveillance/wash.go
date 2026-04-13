package surveillance

import (
	"fmt"
)

// WashTradingDetector detects self-trading attempts.
type WashTradingDetector struct {
	enabled bool
}

// NewWashTradingDetector creates a wash trading detector.
func NewWashTradingDetector(enabled bool) *WashTradingDetector {
	return &WashTradingDetector{enabled: enabled}
}

func (d *WashTradingDetector) Name() string  { return "wash_trading" }
func (d *WashTradingDetector) Enabled() bool { return d.enabled }

func (d *WashTradingDetector) OnEvent(event *Event) []Alert {
	switch event.Type {
	case EventSTPTriggered:
		return []Alert{{
			DetectorName: d.Name(),
			Severity:     "INFO",
			Instrument:   event.Instrument,
			ClientID:     event.ClientID,
			Description:  fmt.Sprintf("self-trade prevented for client %s", event.ClientID),
			Timestamp:    event.Timestamp,
		}}

	case EventTradeExecuted:
		if event.Trade != nil && event.Trade.BuyClientID == event.Trade.SellClientID && event.Trade.BuyClientID != "" {
			ts := event.Timestamp
			if !event.Trade.Timestamp.IsZero() {
				ts = event.Trade.Timestamp
			}
			return []Alert{{
				DetectorName: d.Name(),
				Severity:     "CRITICAL",
				Instrument:   event.Instrument,
				ClientID:     event.Trade.BuyClientID,
				Description:  fmt.Sprintf("wash trade detected: client %s traded with themselves (trade %d)", event.Trade.BuyClientID, event.Trade.ID),
				Timestamp:    ts,
				Evidence: map[string]interface{}{
					"trade_id":      event.Trade.ID,
					"buy_order_id":  event.Trade.BuyOrderID,
					"sell_order_id": event.Trade.SellOrderID,
				},
			}}
		}
	}
	return nil
}
