package hitbtc

import (
	"container/list"
	"log"
	"time"

	jsoniter "github.com/json-iterator/go"
)

type basicRequest struct {
	ID     string      `json:"id"`
	Method string      `json:"method"`
	Params interface{} `json:"params"`
}

type paramsLogin struct {
	Algo      string `json:"algo"`
	Nonce     string `json:"nonce"`
	Signature string `json:"signature"`
	PKey      string `json:"pKey"`
}

type paramsOrder struct {
	ClientOrderID  string `json:"clientOrderId"`
	RequestOrderID string `json:"requestClientId"`
	Symbol         string `json:"symbol"`
	Side           string `json:"side"`
	Type           string `json:"type"`
	TimeInForce    string `json:"timeInForce"`
	Quantity       string `json:"quantity"`
	Price          string `json:"price"`
}

type noParams struct{}

type paramsSymbol struct {
	Symbol string `json:"symbol"`
}

type responseCatcher struct {
	Method  string
	Channel *chan int
}

type responseBalance struct {
	Currency  string `json:"currency"`
	Available string `json:"available"`
	Reserved  string `json:"reserved"`
}

type responseOrder struct {
	ID            string `json:"id"`
	Symbol        string `json:"symbol"`
	ClientOrderID string `json:"clientOrderId"`
	Side          string `json:"side"`
	Status        string `json:"status"`
	Type          string `json:"type"`
	Quantity      string `json:"quantity"`
	Price         string `json:"price"`
	CumQuantity   string `json:"cumQuantity"`
	TradeQuantity string `json:"tradeQuantity"`
	TradePrice    string `json:"tradePrice"`
	TradeFee      string `json:"tradeFee"`
}

type responseOrderbook struct {
	Symbol   string                  `json:"symbol"`
	Sequence int                     `json:"sequence"`
	Ask      []responseOrderbookItem `json:"ask"`
	Bid      []responseOrderbookItem `json:"bid"`
}

type responseOrderbookItem struct {
	Price string `json:"price"`
	Size  string `json:"size"`
}

type responseSymbol struct {
	ID                   string `json:"id"`
	BaseCurrency         string `json:"baseCurrency"`
	QuoteCurrency        string `json:"quoteCurrency"`
	QuantityIncrement    string `json:"quantityIncrement"`
	TickSize             string `json:"tickSize"`
	TakeLiquidityRate    string `json:"takeLiquidityRate"`
	ProvideLiquidityRate string `json:"provideLiquidityRate"`
	FeeCurrency          string `json:"feeCurrency"`
}

type responseTicker struct {
	Ask    string `json:"ask"`
	Bid    string `json:"bid"`
	Symbol string `json:"symbol"`
}

func (C *Client) read() {
	for {
		_, msg, err := C.ws.ReadMessage()
		if err != nil {
			log.Println(err)
			C.Reconnect()
			continue
		}
		C.incoming <- msg
	}
}

func (C *Client) send(payload interface{}) error {
	if err := C.ws.WriteJSON(payload); err != nil {
		log.Println(err)
		return err
	}
	return nil
}

func (C *Client) CopyBooks(depth int) map[string]*Orderbook {
	res := make(map[string]*Orderbook)
	C.bookLock.RLock()
	for k, v := range C.books {
		if len(v.Asks) < depth || len(v.Bids) < depth {
			res[k] = nil
			continue
		}
		res[k] = &Orderbook{
			Asks: v.Asks[:depth],
			Bids: v.Bids[:depth],
			TS:   v.TS,
		}
	}
	C.bookLock.RUnlock()
	return res
}

func (C *Client) worker() {
	var id, method, err string
	for {
		select {
		case msg := <-C.incoming:
			method = jsoniter.Get(msg, "method").ToString()
			if len(method) != 0 {
				if method == "ticker" {
					C.handleTicker(msg)
				} else if method == "snapshotOrderbook" {
					C.handleSnapshotOrderbook(msg)
				} else if method == "updateOrderbook" {
					C.bookUpdates <- msg
				} else if method == "report" {
					log.Println(string(msg))
					resp := responseOrder{}
					jsoniter.Get(msg, "params").ToVal(&resp)
					C.handleOrdersReport(resp)
				}
				continue
			}

			id = jsoniter.Get(msg, "id").ToString()
			err = jsoniter.Get(msg, "error").ToString()
			if len(err) != 0 {
				log.Println("err", string(msg))
			}
			if req, ok := C.requests[id]; ok {
				if len(err) == 0 {
					if req == "getSymbols" {
						C.handleGetSymbols(msg)
					} else if req == "getTradingBalance" {
						C.handleGetBalance(msg)
					} else if req == "getOrders" {
						C.handleGetActiveOrders(msg)
					}
				}
				delete(C.requests, id)
			}
		}
	}
}

func (C *Client) workerUpdateOrderbook() {
	for i := 0; i < 2; i++ {
		go func() {
			for {
				resp := <-C.bookUpdates
				C.handleUpdateOrderbook(resp)
			}
		}()
	}
}

func (C *Client) handleOrdersReport(resp responseOrder) {
	coid := resp.ClientOrderID
	order := Order{
		ClientOrderID: coid,
		Symbol:        resp.Symbol,
		Side:          resp.Side,
		Status:        resp.Status,
		Type:          resp.Type,
		Quantity:      stf(resp.Quantity),
		CumQuantity:   stf(resp.CumQuantity),
		Price:         stf(resp.Price),
		TradePrice:    stf(resp.TradePrice),
		TradeQuantity: stf(resp.TradeQuantity),
		TradeFee:      stf(resp.TradeFee),
	}
	C.orders.Store(coid, order)
}

func (C *Client) handleSnapshotOrderbook(msg []byte) {
	resp := responseOrderbook{}
	jsoniter.Get(msg, "params").ToVal(&resp)
	book, symbol := parseBook(resp, C.MaxBookDepth)
	C.bookLock.Lock()
	C.books[symbol] = book
	C.deferedBooks[symbol] = list.New()
	C.bookLock.Unlock()
}

func (C *Client) handleUpdateOrderbook(msg []byte) {
	resp := responseOrderbook{}
	jsoniter.Get(msg, "params").ToVal(&resp)
	update, symbol := parseBook(resp, 0)
	C.bookLock.RLock()
	book, _ := C.books[symbol]
	defered, _ := C.deferedBooks[symbol]
	C.bookLock.RUnlock()
	defered.PushBack(update)

	if book == nil {
		return
	}

	for u := defered.Front(); u != nil; {
		if book.Seq+1 == u.Value.(*Orderbook).Seq {
			mergeBook(book, u.Value.(*Orderbook), C.MaxBookDepth)
			defered.Remove(u)
			u = defered.Front()
		} else {
			u = u.Next()
		}
	}
	C.bookLock.Lock()
	C.books[symbol] = book
	C.deferedBooks[symbol] = defered
	C.bookLock.Unlock()
}

func (C *Client) handleGetActiveOrders(msg []byte) {
	o := make([]responseOrder, 0, 1)
	log.Println(string(msg))
	jsoniter.Get(msg, "result").ToVal(&o)
	for _, v := range o {
		C.handleOrdersReport(v)
	}
}

func (C *Client) handleGetSymbols(msg []byte) {
	s := make([]responseSymbol, 0, 1)
	jsoniter.Get(msg, "result").ToVal(&s)
	for _, v := range s {
		C.Symbols[v.ID] = Symbol{
			BaseCurrency:         v.BaseCurrency,
			QuoteCurrency:        v.QuoteCurrency,
			QuantityIncrement:    stf(v.QuantityIncrement),
			FeeCurrency:          v.FeeCurrency,
			ProvideLiquidityRate: stf(v.ProvideLiquidityRate),
			TickSize:             stf(v.TickSize),
		}
	}
}

func (C *Client) handleGetBalance(msg []byte) {
	b := make([]responseBalance, 0, 1)
	jsoniter.Get(msg, "result").ToVal(&b)
	for _, v := range b {
		C.balance.Store(v.Currency, BalanceItem{
			stf(v.Available),
			stf(v.Reserved),
		})
	}
}

func (C *Client) handleTicker(msg []byte) {
	t := responseTicker{}
	jsoniter.Get(msg, "params").ToVal(&t)
	C.tickerLock.Lock()
	C.tickers[t.Symbol] = Ticker{
		Ask: stf(t.Ask),
		Bid: stf(t.Bid),
		TS:  time.Now(),
	}
	C.tickerLock.Unlock()
}
