package hitbtc

import (
	"container/list"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	BUY  = "buy"
	SELL = "sell"
	IOC  = "IOC"
	GTC  = "GTC"
	FOK  = "FOK"
)

type BalanceItem = [2]float64
type BookItem = [2]float64

type Book struct {
	Seq  float64
	Asks *list.List
	Bids *list.List
}

type Orderbook struct {
	Seq  float64
	TS   time.Time
	Asks []BookItem
	Bids []BookItem
}

type Client struct {
	APIKey       string
	SecretKey    string
	Symbols      map[string]Symbol
	MaxBookDepth int
	Workers      int
	BookWorkers  int

	balance       *sync.Map
	books         map[string]*Orderbook
	bookLock      sync.RWMutex
	bookUpdates   chan []byte
	deferedBooks  map[string]*list.List
	incoming      chan []byte
	orders        *sync.Map
	requests      map[string]string
	subscriptions *list.List
	tickers       map[string]Ticker
	tickerLock    sync.RWMutex
	ws            *websocket.Conn
}

type JSONMap = map[string]interface{}

type Symbol struct {
	ID                   string  `json:"id"`
	BaseCurrency         string  `json:"baseCurrency"`
	QuoteCurrency        string  `json:"quoteCurrency"`
	QuantityIncrement    float64 `json:"quantityIncrement"`
	TickSize             float64 `json:"tickSize"`
	TakeLiquidityRate    float64 `json:"takeLiquidityRate"`
	ProvideLiquidityRate float64 `json:"provideLiquidityRate"`
	FeeCurrency          string  `json:"feeCurrency"`
}

type Order struct {
	Symbol        string
	ClientOrderID string
	Side          string
	Status        string
	Type          string
	Quantity      float64
	Price         float64
	CumQuantity   float64
	TradeQuantity float64
	TradePrice    float64
	TradeFee      float64
}

type Ticker struct {
	Ask float64
	Bid float64
	TS  time.Time
}

func NewClient() *Client {
	return &Client{
		Symbols:      make(map[string]Symbol),
		MaxBookDepth: 10,
		Workers:      1,
		BookWorkers:  2,

		incoming:      make(chan []byte, 200),
		bookUpdates:   make(chan []byte, 100),
		requests:      make(map[string]string),
		subscriptions: list.New(),
		tickers:       make(map[string]Ticker),
		books:         make(map[string]*Orderbook),
		balance:       new(sync.Map),
		deferedBooks:  make(map[string]*list.List),
		orders:        new(sync.Map),
	}
}

func (C *Client) Login() {
	if len(C.APIKey) == 0 {
		return
	}
	log.Printf("Login with key [%s]\n", C.APIKey)
	nonce := newID()
	h := hmac.New(sha256.New, []byte(C.SecretKey))
	h.Write([]byte(nonce))
	C.send(basicRequest{
		Method: "login",
		ID:     newID(),
		Params: paramsLogin{
			Algo:      "HS256",
			PKey:      C.APIKey,
			Nonce:     nonce,
			Signature: hex.EncodeToString(h.Sum(nil)),
		},
	})
}

func (C *Client) Reconnect() {
loopreconnect:
	if C.ws != nil {
		C.ws.Close()
	}
	log.Println("Connecting...")
	for {
		dialer := websocket.Dialer{
			ReadBufferSize: 40960,
		}
		c, _, err := dialer.Dial("wss://api.hitbtc.com/api/2/ws", nil)
		if err != nil {
			log.Println(err)
			time.Sleep(5 * time.Second)
			C.ws = nil
		} else {
			C.ws = c
			break
		}
	}
	C.Login()
	for e := C.subscriptions.Front(); e != nil; e = e.Next() {
		req := e.Value.(basicRequest)
		req.ID = newID()
		log.Println("Resubscribe ", req)
		if err := C.send(req); err != nil {
			log.Println(err)
			goto loopreconnect
		}
	}
}

func (C *Client) Run() {
	C.Reconnect()
	go C.read()
	for i := 0; i < C.BookWorkers; i++ {
		C.workerUpdateOrderbook()
	}
	for i := 0; i < C.Workers; i++ {
		go C.worker()
	}
}

func (C *Client) GetActiveOrders() {
	id := newID()
	C.requests[id] = "getOrders"
	C.send(basicRequest{
		Method: "getOrders",
		ID:     id,
		Params: noParams{},
	})
}

func (C *Client) GetBalance() {
	id := newID()
	C.requests[id] = "getTradingBalance"
	C.send(basicRequest{
		Method: "getTradingBalance",
		ID:     id,
		Params: noParams{},
	})
}

func (C *Client) GetSymbols() {
	id := newID()
	C.requests[id] = "getSymbols"
	C.send(basicRequest{
		Method: "getSymbols",
		ID:     id,
		Params: noParams{},
	})
}

func (C *Client) SubscribeTickers(symbols []string) {
	for _, symbol := range symbols {
		C.SubscribeTicker(symbol)
	}
}

func (C *Client) SubscribeTicker(symbol string) {
	req := basicRequest{
		Method: "subscribeTicker",
		ID:     newID(),
		Params: paramsSymbol{
			Symbol: symbol,
		},
	}
	C.subscriptions.PushBack(req)
	C.send(req)
}

func (C *Client) SubscribeReports() {
	req := basicRequest{
		Method: "subscribeReports",
		ID:     newID(),
		Params: noParams{},
	}
	C.subscriptions.PushBack(req)
	C.send(req)
}

func (C *Client) SubscribeBooks(symbols []string) {
	for _, symbol := range symbols {
		C.SubscribeBook(symbol)
	}
}

func (C *Client) SubscribeBook(symbol string) {
	req := basicRequest{
		Method: "subscribeOrderbook",
		ID:     newID(),
		Params: paramsSymbol{
			Symbol: symbol,
		},
	}
	C.subscriptions.PushBack(req)
	C.send(req)
}

func (C *Client) PlaceLimit(symbol string, price float64, quantity float64, side string, tif string) string {
	coid := newID()
	id := newID()
	C.send(basicRequest{
		Method: "newOrder",
		ID:     id,
		Params: paramsOrder{
			Symbol:        symbol,
			Side:          side,
			Type:          "limit",
			TimeInForce:   tif,
			ClientOrderID: coid,
			Price:         fmt.Sprintf("%.12f", price),
			Quantity:      fmt.Sprintf("%.12f", quantity),
		},
	})

	return coid
}

func (C *Client) UpdateOrder(coid string, price float64, quantity float64) string {
	newcoid := newID()
	C.send(basicRequest{
		Method: "cancelReplaceOrder",
		ID:     newID(),
		Params: paramsOrder{
			RequestOrderID: newcoid,
			ClientOrderID:  coid,
			Price:          fmt.Sprintf("%.12f", price),
			Quantity:       fmt.Sprintf("%.12f", quantity),
		},
	})

	return newcoid
}

func (C *Client) CancelOrder(coid string) {
	C.send(basicRequest{
		Method: "cancelOrder",
		ID:     newID(),
		Params: paramsOrder{
			ClientOrderID: coid,
		},
	})
}

func (C *Client) Balance(symbol string) BalanceItem {
	b, ok := C.balance.Load(symbol)
	if !ok {
		return BalanceItem{0.0, 0.0}
	}
	return b.(BalanceItem)
}

func (C *Client) Book(symbol string) *Orderbook {
	C.bookLock.RLock()
	b, ok := C.books[symbol]
	C.bookLock.RUnlock()
	if !ok {
		return nil
	}
	return b
}

func (C *Client) Order(coid string) Order {
	o, ok := C.orders.Load(coid)
	if !ok {
		return Order{
			Status: "unknown",
		}
	}
	return o.(Order)
}

func (C *Client) Ticker(symbol string) Ticker {
	C.tickerLock.RLock()
	t, ok := C.tickers[symbol]
	C.tickerLock.RUnlock()
	if !ok {
		return Ticker{
			Bid: 0,
			Ask: math.Inf(-1),
		}
	}
	return t
}
