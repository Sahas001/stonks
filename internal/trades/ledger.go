package trades

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Entry struct {
	RecordedAt time.Time `json:"recorded_at"`
	Symbol     string    `json:"symbol"`
	Side       string    `json:"side"`
	Quantity   float64   `json:"quantity"`
	Price      float64   `json:"price"`
	Note       string    `json:"note,omitempty"`
}

type Position struct {
	Symbol        string
	Quantity      float64
	AverageCost   float64
	CostBasis     float64
	MarketPrice   float64
	MarketValue   float64
	UnrealizedPnL float64
	UnrealizedPct float64
}

type ClosedTrade struct {
	Symbol      string
	Quantity    float64
	AverageBuy  float64
	SellPrice   float64
	RealizedPnL float64
	RealizedPct float64
	ClosedAt    time.Time
}

type Summary struct {
	Entries         []Entry
	Positions       []Position
	ClosedTrades    []ClosedTrade
	RealizedPnL     float64
	UnrealizedPnL   float64
	MarketValue     float64
	InvestedCapital float64
	OpenPositions   int
}

func Record(path, side, symbol string, quantity, price float64, note string, now time.Time) (Entry, error) {
	if quantity <= 0 {
		return Entry{}, fmt.Errorf("quantity must be positive")
	}
	if price <= 0 {
		return Entry{}, fmt.Errorf("price must be positive")
	}
	side = strings.ToLower(strings.TrimSpace(side))
	if side != "buy" && side != "sell" {
		return Entry{}, fmt.Errorf("side must be buy or sell")
	}

	entry := Entry{
		RecordedAt: now.UTC(),
		Symbol:     strings.ToUpper(strings.TrimSpace(symbol)),
		Side:       side,
		Quantity:   quantity,
		Price:      price,
		Note:       strings.TrimSpace(note),
	}

	entries, err := load(path)
	if err != nil {
		return Entry{}, err
	}
	entries = append(entries, entry)
	return entry, save(path, entries)
}

func Load(path string) ([]Entry, error) {
	return load(path)
}

func Summarize(entries []Entry, latestQuotes map[string]float64) Summary {
	type lot struct {
		qty   float64
		price float64
	}
	lots := make(map[string][]lot)
	summary := Summary{
		Entries:      entries,
		Positions:    make([]Position, 0),
		ClosedTrades: make([]ClosedTrade, 0),
	}

	for _, entry := range entries {
		switch entry.Side {
		case "buy":
			lots[entry.Symbol] = append(lots[entry.Symbol], lot{qty: entry.Quantity, price: entry.Price})
		case "sell":
			remaining := entry.Quantity
			consumedQty := 0.0
			consumedCost := 0.0
			currentLots := lots[entry.Symbol]
			nextLots := make([]lot, 0, len(currentLots))
			for _, item := range currentLots {
				if remaining <= 0 {
					nextLots = append(nextLots, item)
					continue
				}
				useQty := min(item.qty, remaining)
				if useQty > 0 {
					consumedQty += useQty
					consumedCost += useQty * item.price
					item.qty -= useQty
					remaining -= useQty
				}
				if item.qty > 0 {
					nextLots = append(nextLots, item)
				}
			}
			lots[entry.Symbol] = nextLots
			if consumedQty > 0 {
				avgBuy := consumedCost / consumedQty
				realized := (entry.Price - avgBuy) * consumedQty
				realizedPct := 0.0
				if avgBuy > 0 {
					realizedPct = ((entry.Price - avgBuy) / avgBuy) * 100
				}
				summary.ClosedTrades = append(summary.ClosedTrades, ClosedTrade{
					Symbol:      entry.Symbol,
					Quantity:    consumedQty,
					AverageBuy:  avgBuy,
					SellPrice:   entry.Price,
					RealizedPnL: realized,
					RealizedPct: realizedPct,
					ClosedAt:    entry.RecordedAt,
				})
				summary.RealizedPnL += realized
			}
		}
	}

	for symbol, symbolLots := range lots {
		totalQty := 0.0
		costBasis := 0.0
		for _, item := range symbolLots {
			totalQty += item.qty
			costBasis += item.qty * item.price
		}
		if totalQty <= 0 {
			continue
		}
		marketPrice := latestQuotes[symbol]
		avgCost := costBasis / totalQty
		marketValue := totalQty * marketPrice
		unrealized := marketValue - costBasis
		unrealizedPct := 0.0
		if costBasis > 0 {
			unrealizedPct = (unrealized / costBasis) * 100
		}
		summary.Positions = append(summary.Positions, Position{
			Symbol:        symbol,
			Quantity:      totalQty,
			AverageCost:   avgCost,
			CostBasis:     costBasis,
			MarketPrice:   marketPrice,
			MarketValue:   marketValue,
			UnrealizedPnL: unrealized,
			UnrealizedPct: unrealizedPct,
		})
		summary.OpenPositions++
		summary.InvestedCapital += costBasis
		summary.MarketValue += marketValue
		summary.UnrealizedPnL += unrealized
	}

	sort.Slice(summary.Positions, func(i, j int) bool {
		return summary.Positions[i].Symbol < summary.Positions[j].Symbol
	})
	sort.Slice(summary.ClosedTrades, func(i, j int) bool {
		return summary.ClosedTrades[i].ClosedAt.Before(summary.ClosedTrades[j].ClosedAt)
	})
	return summary
}

func load(path string) ([]Entry, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	entries := make([]Entry, 0)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry Entry
		if err := json.Unmarshal(line, &entry); err == nil {
			entries = append(entries, entry)
		}
	}
	return entries, scanner.Err()
}

func save(path string, entries []Entry) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(abs, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	for _, entry := range entries {
		if err := encoder.Encode(entry); err != nil {
			return err
		}
	}
	return nil
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
