package journal

import (
	"path/filepath"
	"testing"
	"time"

	"stonks/internal/domain"
)

func TestPerformanceReportSkipsUnchangedDuplicateSnapshots(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "journal.jsonl")
	now := time.Date(2026, 3, 29, 10, 0, 0, 0, time.UTC)

	entries := []Entry{
		{
			RecordedAt: now,
			EntryType:  "bot_pick",
			Recommendations: []domain.Recommendation{
				{Symbol: "AKJCL", BuyPrice: 415, StopLoss: 399, TakeProfit: 472.6},
			},
			LatestQuotes: map[string]float64{"AKJCL": 415},
		},
		{
			RecordedAt:   now.Add(5 * time.Minute),
			EntryType:    "bot_pick",
			LatestQuotes: map[string]float64{"AKJCL": 415},
		},
		{
			RecordedAt:   now.Add(24 * time.Hour),
			EntryType:    "bot_pick",
			LatestQuotes: map[string]float64{"AKJCL": 430},
		},
	}
	if err := save(path, entries); err != nil {
		t.Fatalf("save journal: %v", err)
	}

	report, err := PerformanceReport(path)
	if err != nil {
		t.Fatalf("performance report: %v", err)
	}
	if report == nil {
		t.Fatal("expected report")
	}
	if report.PicksEvaluated != 1 {
		t.Fatalf("expected 1 evaluated pick, got %d", report.PicksEvaluated)
	}
	if report.AverageChangePct <= 0 {
		t.Fatalf("expected positive change, got %.2f", report.AverageChangePct)
	}
	if report.BestPick == nil || report.BestPick.CurrentPrice != 430 {
		t.Fatalf("expected best pick current price 430, got %+v", report.BestPick)
	}
}

func TestRecordSuppressesDuplicateBotPicksWithinWindow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "journal.jsonl")
	base := time.Date(2026, 3, 29, 10, 0, 0, 0, time.UTC)
	result := domain.AnalysisResult{
		MarketStatus: "fallback_candidates",
		RecommendationSet: []domain.Recommendation{
			{Symbol: "AKJCL", Mode: "hybrid", Verdict: "watchlist", BuyPrice: 415, StopLoss: 399, TakeProfit: 472.6},
		},
		LatestQuotes: map[string]float64{"AKJCL": 415},
		AnalyzedAt:   base,
	}

	if _, err := Record(path, 14, 120, 60, "bot_pick", result); err != nil {
		t.Fatalf("first record: %v", err)
	}

	result.AnalyzedAt = base.Add(30 * time.Minute)
	if _, err := Record(path, 14, 120, 60, "bot_pick", result); err != nil {
		t.Fatalf("duplicate record: %v", err)
	}

	entries, err := load(path)
	if err != nil {
		t.Fatalf("load journal: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after duplicate suppression, got %d", len(entries))
	}
	if !entries[0].RecordedAt.Equal(result.AnalyzedAt) {
		t.Fatalf("expected latest timestamp to replace duplicate entry, got %s", entries[0].RecordedAt)
	}

	result.AnalyzedAt = base.Add(2 * time.Hour)
	if _, err := Record(path, 14, 120, 60, "bot_pick", result); err != nil {
		t.Fatalf("later record: %v", err)
	}
	entries, err = load(path)
	if err != nil {
		t.Fatalf("reload journal: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected second entry after append window, got %d", len(entries))
	}
}
