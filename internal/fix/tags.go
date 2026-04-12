// Package fix implements a minimal FIX 4.4 protocol gateway.
package fix

// FIX tag constants
const (
	TagBeginString  = 8
	TagBodyLength   = 9
	TagCheckSum     = 10
	TagClOrdID      = 11
	TagCumQty       = 14
	TagExecID       = 17
	TagLastPx       = 31
	TagLastQty      = 32
	TagMsgSeqNum    = 34
	TagMsgType      = 35
	TagOrderID      = 37
	TagOrderQty     = 38
	TagOrdStatus    = 39
	TagOrdType      = 40
	TagOrigClOrdID  = 41
	TagPrice        = 44
	TagSenderCompID = 49
	TagSendingTime  = 52
	TagSide         = 54
	TagSymbol       = 55
	TagTargetCompID = 56
	TagExecType     = 150
	TagLeavesQty    = 151
	TagHeartBtInt   = 108
)

// FIX message types
const (
	MsgTypeHeartbeat          = "0"
	MsgTypeTestRequest        = "1"
	MsgTypeLogon              = "A"
	MsgTypeLogout             = "5"
	MsgTypeNewOrderSingle     = "D"
	MsgTypeOrderCancelRequest = "F"
	MsgTypeExecutionReport    = "8"
)

// FIX side values
const (
	SideBuy  = "1"
	SideSell = "2"
)

// FIX order type values
const (
	OrdTypeMarket = "1"
	OrdTypeLimit  = "2"
)

// FIX execution types
const (
	ExecTypeNew       = "0"
	ExecTypeCancelled = "4"
	ExecTypeTrade     = "F"
	ExecTypeRejected  = "8"
)

// FIX order status
const (
	OrdStatusNew             = "0"
	OrdStatusPartiallyFilled = "1"
	OrdStatusFilled          = "2"
	OrdStatusCancelled       = "4"
	OrdStatusRejected        = "8"
)

// BeginString for FIX 4.4
const BeginString = "FIX.4.4"
