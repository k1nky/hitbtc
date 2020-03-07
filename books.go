package hitbtc

import (
	"time"
)

func parseBook(resp responseOrderbook, maxLength int) (*Orderbook, string) {
	book := &Orderbook{
		Asks: make([]BookItem, 0, 10),
		Bids: make([]BookItem, 0, 10),
		TS:   time.Now(),
	}
	book.Seq = float64(resp.Sequence)
	for _, v := range resp.Ask {
		book.Asks = append(book.Asks, BookItem{stf(v.Price), stf(v.Size)})
		if maxLength != 0 && len(book.Asks) > maxLength {
			break
		}
	}
	for _, v := range resp.Bid {
		book.Bids = append(book.Bids, BookItem{stf(v.Price), stf(v.Size)})
		if maxLength != 0 && len(book.Bids) > maxLength {
			break
		}
	}
	return book, resp.Symbol
}

func mergeBook(orig *Orderbook, update *Orderbook, maxLength int) {

	for _, u := range update.Asks {
		for oi, o := range orig.Asks {
			if o[0] == u[0] {
				if u[1] == 0.0 {
					if oi < len(orig.Asks)-1 {
						copy(orig.Asks[oi:], orig.Asks[oi+1:])
					}
					orig.Asks[len(orig.Asks)-1] = BookItem{0.0, 0.0}
					orig.Asks = orig.Asks[:len(orig.Asks)-1]
				} else {
					orig.Asks[oi] = u
				}
				goto loopasksmerge
			} else if u[0] < o[0] {
				if u[1] != 0.0 {
					orig.Asks = append(orig.Asks, BookItem{0.0, 0.0})
					copy(orig.Asks[oi+1:], orig.Asks[oi:])
					orig.Asks[oi] = u
				}
				goto loopasksmerge
			}
		}
		if u[1] != 0.0 && len(orig.Asks) < maxLength {
			orig.Asks = append(orig.Asks, u)
		}
	loopasksmerge:
	}
	if len(orig.Asks) > maxLength {
		orig.Asks = orig.Asks[:maxLength]
	}

	for _, u := range update.Bids {
		for oi, o := range orig.Bids {
			if o[0] == u[0] {
				if u[1] == 0.0 {
					if oi < len(orig.Bids)-1 {
						copy(orig.Bids[oi:], orig.Bids[oi+1:])
					}
					orig.Bids[len(orig.Bids)-1] = BookItem{0.0, 0.0}
					orig.Bids = orig.Bids[:len(orig.Bids)-1]
				} else {
					orig.Bids[oi] = u
				}
				goto loopbidsmerge
			} else if u[0] > o[0] {
				if u[1] != 0.0 {
					orig.Bids = append(orig.Bids, BookItem{0.0, 0.0})
					copy(orig.Bids[oi+1:], orig.Bids[oi:])
					orig.Bids[oi] = u
				}
				goto loopbidsmerge
			}
		}
		if u[1] != 0.0 && len(orig.Bids) < maxLength {
			orig.Bids = append(orig.Bids, u)
		}
	loopbidsmerge:
	}
	if len(orig.Bids) > maxLength {
		orig.Bids = orig.Bids[:maxLength]
	}

	orig.Seq = update.Seq
}
