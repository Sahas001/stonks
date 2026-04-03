package analyzer

import (
	"context"
	"testing"
	"time"

	"stonks/internal/config"
	"stonks/internal/domain"
	"stonks/internal/source"
)

type stubSource struct {
	name     string
	snapshot domain.MarketSnapshot
	candles  map[string][]domain.Candle
}

func (s stubSource) Name() string { return s.name }

func (s stubSource) Fetch(context.Context) (domain.MarketSnapshot, error) {
	return s.snapshot, nil
}

func (s stubSource) FetchCandles(_ context.Context, symbol, resolution string, frame int) ([]domain.Candle, error) {
	return s.candles[symbol], nil
}

func TestAnalyzeReturnsRecommendation(t *testing.T) {
	cfg := config.Config{
		TopN:                3,
		MinRiskReward:       1.5,
		MaxRiskPerTradePct:  3,
		DefaultTakeProfitRR: 2.2,
		MinQuoteVolume:      1000,
		MinTradeCount:       3,
	}

	engine := New(cfg, []source.Source{stubSource{
		name: "stub",
		snapshot: domain.MarketSnapshot{
			Quotes: []domain.Quote{{
				Symbol:    "NABIL",
				LastPrice: 520,
				OpenPrice: 500,
				HighPrice: 530,
				LowPrice:  495,
				PrevClose: 498,
				Volume:    25000,
				Turnover:  13000000,
			}},
			Trades: []domain.Trade{
				{Symbol: "NABIL", Price: 518, Quantity: 100},
				{Symbol: "NABIL", Price: 520, Quantity: 120},
				{Symbol: "NABIL", Price: 522, Quantity: 180},
				{Symbol: "NABIL", Price: 525, Quantity: 140},
			},
		},
		candles: map[string][]domain.Candle{
			"NABIL": {
				{Symbol: "NABIL", Close: 500, Volume: 1000, Timestamp: time.Now().AddDate(0, 0, -5)},
				{Symbol: "NABIL", Close: 505, Volume: 1200, Timestamp: time.Now().AddDate(0, 0, -4)},
				{Symbol: "NABIL", Close: 510, Volume: 1300, Timestamp: time.Now().AddDate(0, 0, -3)},
				{Symbol: "NABIL", Close: 515, Volume: 1250, Timestamp: time.Now().AddDate(0, 0, -2)},
				{Symbol: "NABIL", Close: 520, Volume: 1400, Timestamp: time.Now().AddDate(0, 0, -1)},
			},
		},
	}})

	result, err := engine.Analyze(context.Background(), ModeFloorsheet)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if len(result.RecommendationSet) != 1 {
		t.Fatalf("expected 1 recommendation, got %d", len(result.RecommendationSet))
	}
	if result.RecommendationSet[0].RiskReward < cfg.MinRiskReward {
		t.Fatalf("expected risk reward >= %.2f, got %.2f", cfg.MinRiskReward, result.RecommendationSet[0].RiskReward)
	}
}

func TestAnalyzeSkipsInactiveTicker(t *testing.T) {
	cfg := config.Config{
		TopN:                 3,
		MinRiskReward:        1.5,
		MaxRiskPerTradePct:   3,
		DefaultTakeProfitRR:  2.2,
		MinQuoteVolume:       1000,
		MinTradeCount:        3,
		InactiveLookbackDays: 120,
		InactiveMinTradeDays: 5,
		HistoryResolution:    "1D",
		HistoryFrame:         300,
	}

	staleDay := time.Now().AddDate(0, 0, -200)
	engine := New(cfg, []source.Source{stubSource{
		name: "stub",
		snapshot: domain.MarketSnapshot{
			Quotes: []domain.Quote{{
				Symbol:    "OLDCO",
				LastPrice: 120,
				OpenPrice: 118,
				HighPrice: 122,
				LowPrice:  117,
				PrevClose: 119,
				Volume:    50000,
				Turnover:  6000000,
			}},
			Trades: []domain.Trade{
				{Symbol: "OLDCO", Price: 119, Quantity: 100},
				{Symbol: "OLDCO", Price: 120, Quantity: 120},
				{Symbol: "OLDCO", Price: 121, Quantity: 130},
			},
		},
		candles: map[string][]domain.Candle{
			"OLDCO": {
				{Symbol: "OLDCO", Close: 100, Volume: 1000, Timestamp: staleDay},
				{Symbol: "OLDCO", Close: 101, Volume: 1000, Timestamp: staleDay.AddDate(0, 0, 1)},
				{Symbol: "OLDCO", Close: 102, Volume: 1000, Timestamp: staleDay.AddDate(0, 0, 2)},
				{Symbol: "OLDCO", Close: 103, Volume: 1000, Timestamp: staleDay.AddDate(0, 0, 3)},
				{Symbol: "OLDCO", Close: 104, Volume: 1000, Timestamp: staleDay.AddDate(0, 0, 4)},
			},
		},
	}})

	result, err := engine.Analyze(context.Background(), ModeFundamental)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if len(result.RecommendationSet) != 0 {
		t.Fatalf("expected inactive ticker to be rejected, got %d recommendations", len(result.RecommendationSet))
	}
	if len(result.DiscardedSymbols) == 0 {
		t.Fatalf("expected discarded reason for inactive ticker")
	}
}
