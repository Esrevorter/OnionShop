package server

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"net/http"
	"sync/atomic"
	"time"
)

type liveRates struct {
	EUR float64
	AUD float64
	at  time.Time
}

var liveRatesStore atomic.Value

func liveRate(currency string) (float64, bool) {
	v := liveRatesStore.Load()
	if v == nil { return 0, false }
	lr, ok := v.(*liveRates)
	if !ok || time.Since(lr.at) > 60*time.Minute { return 0, false }
	switch currency {
	case "EUR": return lr.EUR, lr.EUR > 0
	case "AUD": return lr.AUD, lr.AUD > 0
	}
	return 0, false
}

func formatFiatWith(cents int64, mult float64, currency string) string {
	target := int64(math.Round(float64(cents) * mult))
	sign := ""
	if target < 0 { sign, target = "-", -target }
	sym := "$"
	switch currency {
	case "EUR": sym = "€"
	case "AUD": sym = "A$"
	}
	return sign + sym + itoa(target/100) + "." + pad2(target%100)
}

func itoa(v int64) string {
	if v == 0 { return "0" }
	out := ""
	for v > 0 { out = string(rune('0'+v%10)) + out; v /= 10 }
	return out
}

func pad2(v int64) string {
	if v < 10 { return "0" + itoa(v) }
	return itoa(v)
}

func StartLiveRates(ctx context.Context) {
	fetch := func() {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(cctx, http.MethodGet, "https://open.er-api.com/v6/latest/USD", nil)
		if err != nil { return }
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Printf("fx: refresh failed: %v", err)
			return
		}
		defer resp.Body.Close()
		var payload struct {
			Rates map[string]float64 `json:"rates"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil { return }
		eur, ok1 := payload.Rates["EUR"]
		aud, ok2 := payload.Rates["AUD"]
		if !ok1 || !ok2 || eur <= 0 || aud <= 0 { return }
		liveRatesStore.Store(&liveRates{EUR: eur, AUD: aud, at: time.Now()})
		log.Printf("fx: live rates updated EUR=%f AUD=%f", eur, aud)
	}
	go func() {
		fetch()
		t := time.NewTicker(15 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done(): return
			case <-t.C: fetch()
			}
		}
	}()
}
