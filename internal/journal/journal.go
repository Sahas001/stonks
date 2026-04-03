package journal

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"stonks/internal/domain"
)

type Entry struct {
	RecordedAt      time.Time               `json:"recorded_at"`
	EntryType       string                  `json:"entry_type,omitempty"`
	MarketStatus    string                  `json:"market_status"`
	Recommendations []domain.Recommendation `json:"recommendations"`
	LatestQuotes    map[string]float64      `json:"latest_quotes,omitempty"`
}

type Review struct {
	ComparedAt         time.Time      `json:"compared_at"`
	PreviousRunAt      time.Time      `json:"previous_run_at,omitempty"`
	EvaluatedCount     int            `json:"evaluated_count"`
	AboveBuyCount      int            `json:"above_buy_count"`
	HitTakeProfitCount int            `json:"hit_take_profit_count"`
	HitStopLossCount   int            `json:"hit_stop_loss_count"`
	Summary            string         `json:"summary"`
	Items              []ReviewedPick `json:"items,omitempty"`
}

type ReviewedPick struct {
	Symbol       string  `json:"symbol"`
	BuyPrice     float64 `json:"buy_price"`
	StopLoss     float64 `json:"stop_loss"`
	TakeProfit   float64 `json:"take_profit"`
	CurrentPrice float64 `json:"current_price"`
	ChangePct    float64 `json:"change_pct"`
	Status       string  `json:"status"`
}

type Performance struct {
	EntriesConsidered   int            `json:"entries_considered"`
	PicksEvaluated      int            `json:"picks_evaluated"`
	AboveBuyCount       int            `json:"above_buy_count"`
	HitTakeProfitCount  int            `json:"hit_take_profit_count"`
	HitStopLossCount    int            `json:"hit_stop_loss_count"`
	AverageChangePct    float64        `json:"average_change_pct"`
	MedianChangePct     float64        `json:"median_change_pct"`
	WinRatePct          float64        `json:"win_rate_pct"`
	TakeProfitRatePct   float64        `json:"take_profit_rate_pct"`
	StopLossRatePct     float64        `json:"stop_loss_rate_pct"`
	BestPick            *ReviewedPick  `json:"best_pick,omitempty"`
	WorstPick           *ReviewedPick  `json:"worst_pick,omitempty"`
	LatestReviewedRunAt time.Time      `json:"latest_reviewed_run_at,omitempty"`
	Items               []ReviewedPick `json:"items,omitempty"`
}

type Summary struct {
	UpdatedAt          time.Time     `json:"updated_at"`
	RunsCompared       int           `json:"runs_compared"`
	PicksEvaluated     int           `json:"picks_evaluated"`
	AboveBuyCount      int           `json:"above_buy_count"`
	HitTakeProfitCount int           `json:"hit_take_profit_count"`
	HitStopLossCount   int           `json:"hit_stop_loss_count"`
	AverageChangePct   float64       `json:"average_change_pct"`
	MedianChangePct    float64       `json:"median_change_pct"`
	WinRatePct         float64       `json:"win_rate_pct"`
	TakeProfitRatePct  float64       `json:"take_profit_rate_pct"`
	StopLossRatePct    float64       `json:"stop_loss_rate_pct"`
	BestPick           *ReviewedPick `json:"best_pick,omitempty"`
	WorstPick          *ReviewedPick `json:"worst_pick,omitempty"`
	LastReviewedRunAt  time.Time     `json:"last_reviewed_run_at,omitempty"`
}

func Record(path string, retentionDays, maxEntries, pickAppendMinutes int, entryType string, result domain.AnalysisResult) (*Review, error) {
	entries, err := load(path)
	if err != nil {
		return nil, err
	}

	var review *Review
	if len(entries) > 0 {
		latestQuotes := result.LatestQuotes
		if previous := previousComparableBotPick(entries, entryType, result, pickAppendMinutes); previous != nil {
			review = evaluate(*previous, latestQuotes, result.AnalyzedAt)
		}
	}

	entry := Entry{
		RecordedAt:      result.AnalyzedAt,
		EntryType:       entryType,
		MarketStatus:    result.MarketStatus,
		Recommendations: result.RecommendationSet,
		LatestQuotes:    result.LatestQuotes,
	}
	entries = upsertEntry(entries, entry, pickAppendMinutes)
	entries = prune(entries, retentionDays, maxEntries, result.AnalyzedAt)
	if err := save(path, entries); err != nil {
		return review, err
	}
	return review, nil
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

func prune(entries []Entry, retentionDays, maxEntries int, now time.Time) []Entry {
	if retentionDays > 0 {
		cutoff := now.AddDate(0, 0, -retentionDays)
		filtered := make([]Entry, 0, len(entries))
		for _, entry := range entries {
			if entry.RecordedAt.IsZero() || entry.RecordedAt.After(cutoff) {
				filtered = append(filtered, entry)
			}
		}
		entries = filtered
	}
	if maxEntries > 0 && len(entries) > maxEntries {
		entries = entries[len(entries)-maxEntries:]
	}
	return entries
}

func evaluate(previous Entry, latestQuotes map[string]float64, now time.Time) *Review {
	if len(previous.Recommendations) == 0 || len(latestQuotes) == 0 {
		return nil
	}
	review := &Review{
		ComparedAt:    now,
		PreviousRunAt: previous.RecordedAt,
		Items:         make([]ReviewedPick, 0, len(previous.Recommendations)),
	}
	for _, rec := range previous.Recommendations {
		current, ok := latestQuotes[rec.Symbol]
		if !ok || current <= 0 {
			continue
		}
		item := ReviewedPick{
			Symbol:       rec.Symbol,
			BuyPrice:     rec.BuyPrice,
			StopLoss:     rec.StopLoss,
			TakeProfit:   rec.TakeProfit,
			CurrentPrice: current,
			ChangePct:    percentChange(rec.BuyPrice, current),
			Status:       status(rec, current),
		}
		review.EvaluatedCount++
		if current > rec.BuyPrice {
			review.AboveBuyCount++
		}
		if item.Status == "take_profit_hit" {
			review.HitTakeProfitCount++
		}
		if item.Status == "stop_loss_hit" {
			review.HitStopLossCount++
		}
		review.Items = append(review.Items, item)
	}
	if review.EvaluatedCount == 0 {
		return nil
	}
	review.Summary = buildSummary(review)
	return review
}

func PerformanceReport(path string) (*Performance, error) {
	entries, err := load(path)
	if err != nil {
		return nil, err
	}
	if len(entries) < 2 {
		return nil, nil
	}

	items := make([]ReviewedPick, 0)
	entriesConsidered := 0
	var latestReviewedRunAt time.Time
	for i := 0; i < len(entries); i++ {
		if entries[i].EntryType != "bot_pick" {
			continue
		}
		nextEntry := nextMeaningfulEntry(entries, i)
		if nextEntry == nil {
			continue
		}
		review := evaluate(entries[i], nextEntry.LatestQuotes, nextEntry.RecordedAt)
		if review == nil || len(review.Items) == 0 {
			continue
		}
		entriesConsidered++
		latestReviewedRunAt = nextEntry.RecordedAt
		items = append(items, review.Items...)
	}
	if len(items) == 0 {
		return nil, nil
	}

	perf := &Performance{
		EntriesConsidered:   entriesConsidered,
		PicksEvaluated:      len(items),
		Items:               items,
		LatestReviewedRunAt: latestReviewedRunAt,
	}
	changes := make([]float64, 0, len(items))
	for i := range items {
		item := items[i]
		changes = append(changes, item.ChangePct)
		if item.CurrentPrice > item.BuyPrice {
			perf.AboveBuyCount++
		}
		if item.Status == "take_profit_hit" {
			perf.HitTakeProfitCount++
		}
		if item.Status == "stop_loss_hit" {
			perf.HitStopLossCount++
		}
		if perf.BestPick == nil || item.ChangePct > perf.BestPick.ChangePct {
			copyItem := item
			perf.BestPick = &copyItem
		}
		if perf.WorstPick == nil || item.ChangePct < perf.WorstPick.ChangePct {
			copyItem := item
			perf.WorstPick = &copyItem
		}
		perf.AverageChangePct += item.ChangePct
	}
	perf.AverageChangePct /= float64(len(items))
	sort.Float64s(changes)
	mid := len(changes) / 2
	if len(changes)%2 == 0 {
		perf.MedianChangePct = (changes[mid-1] + changes[mid]) / 2
	} else {
		perf.MedianChangePct = changes[mid]
	}
	perf.WinRatePct = pct(perf.AboveBuyCount, perf.PicksEvaluated)
	perf.TakeProfitRatePct = pct(perf.HitTakeProfitCount, perf.PicksEvaluated)
	perf.StopLossRatePct = pct(perf.HitStopLossCount, perf.PicksEvaluated)
	return perf, nil
}

func previousComparableBotPick(entries []Entry, entryType string, result domain.AnalysisResult, pickAppendMinutes int) *Entry {
	if entryType != "bot_pick" || len(entries) == 0 {
		return nil
	}
	last := entries[len(entries)-1]
	if last.EntryType != "bot_pick" {
		return nil
	}
	if sameRecommendations(last.Recommendations, result.RecommendationSet) && withinAppendWindow(last.RecordedAt, result.AnalyzedAt, pickAppendMinutes) {
		return nil
	}
	return &last
}

func upsertEntry(entries []Entry, entry Entry, pickAppendMinutes int) []Entry {
	if entry.EntryType != "bot_pick" || len(entries) == 0 {
		return append(entries, entry)
	}
	lastIdx := len(entries) - 1
	last := entries[lastIdx]
	if last.EntryType == "bot_pick" &&
		sameRecommendations(last.Recommendations, entry.Recommendations) &&
		withinAppendWindow(last.RecordedAt, entry.RecordedAt, pickAppendMinutes) {
		entries[lastIdx] = entry
		return entries
	}
	return append(entries, entry)
}

func nextMeaningfulEntry(entries []Entry, currentIdx int) *Entry {
	current := entries[currentIdx]
	for i := currentIdx + 1; i < len(entries); i++ {
		if len(entries[i].LatestQuotes) == 0 {
			continue
		}
		if quotesChangedForRecommendations(current, entries[i].LatestQuotes) {
			return &entries[i]
		}
	}
	return nil
}

func quotesChangedForRecommendations(entry Entry, nextQuotes map[string]float64) bool {
	if len(entry.Recommendations) == 0 || len(nextQuotes) == 0 {
		return false
	}
	for _, rec := range entry.Recommendations {
		next, ok := nextQuotes[rec.Symbol]
		if !ok || next <= 0 {
			continue
		}
		current := entry.LatestQuotes[rec.Symbol]
		if current <= 0 || current != next {
			return true
		}
	}
	return false
}

func sameRecommendations(left, right []domain.Recommendation) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].Symbol != right[i].Symbol ||
			left[i].Mode != right[i].Mode ||
			left[i].Verdict != right[i].Verdict ||
			left[i].BuyPrice != right[i].BuyPrice ||
			left[i].StopLoss != right[i].StopLoss ||
			left[i].TakeProfit != right[i].TakeProfit {
			return false
		}
	}
	return true
}

func withinAppendWindow(previous, current time.Time, pickAppendMinutes int) bool {
	if pickAppendMinutes <= 0 || previous.IsZero() || current.IsZero() || current.Before(previous) {
		return false
	}
	return current.Sub(previous) < time.Duration(pickAppendMinutes)*time.Minute
}

func UpdateSummary(path string, review *Review) error {
	if review == nil || len(review.Items) == 0 {
		return nil
	}

	summary, err := loadSummary(path)
	if err != nil {
		return err
	}
	if summary == nil {
		summary = &Summary{}
	}

	summary.UpdatedAt = review.ComparedAt
	summary.RunsCompared++
	summary.PicksEvaluated += review.EvaluatedCount
	summary.AboveBuyCount += review.AboveBuyCount
	summary.HitTakeProfitCount += review.HitTakeProfitCount
	summary.HitStopLossCount += review.HitStopLossCount
	summary.LastReviewedRunAt = review.ComparedAt

	totalChange := summary.AverageChangePct * float64(maxInt(summary.PicksEvaluated-review.EvaluatedCount, 0))
	for _, item := range review.Items {
		totalChange += item.ChangePct
		if summary.BestPick == nil || item.ChangePct > summary.BestPick.ChangePct {
			copyItem := item
			summary.BestPick = &copyItem
		}
		if summary.WorstPick == nil || item.ChangePct < summary.WorstPick.ChangePct {
			copyItem := item
			summary.WorstPick = &copyItem
		}
	}
	if summary.PicksEvaluated > 0 {
		summary.AverageChangePct = totalChange / float64(summary.PicksEvaluated)
	}
	summary.WinRatePct = pct(summary.AboveBuyCount, summary.PicksEvaluated)
	summary.TakeProfitRatePct = pct(summary.HitTakeProfitCount, summary.PicksEvaluated)
	summary.StopLossRatePct = pct(summary.HitStopLossCount, summary.PicksEvaluated)

	changes := make([]float64, 0, len(review.Items))
	for _, item := range review.Items {
		changes = append(changes, item.ChangePct)
	}
	sort.Float64s(changes)
	if len(changes) > 0 {
		mid := len(changes) / 2
		if len(changes)%2 == 0 {
			summary.MedianChangePct = (changes[mid-1] + changes[mid]) / 2
		} else {
			summary.MedianChangePct = changes[mid]
		}
	}
	return saveSummary(path, summary)
}

func SummaryReport(path string) (*Summary, error) {
	return loadSummary(path)
}

func RebuildSummary(journalPath, summaryPath string) (*Summary, error) {
	perf, err := PerformanceReport(journalPath)
	if err != nil {
		return nil, err
	}
	if perf == nil {
		return nil, nil
	}
	summary := &Summary{
		UpdatedAt:          perf.LatestReviewedRunAt,
		RunsCompared:       perf.EntriesConsidered,
		PicksEvaluated:     perf.PicksEvaluated,
		AboveBuyCount:      perf.AboveBuyCount,
		HitTakeProfitCount: perf.HitTakeProfitCount,
		HitStopLossCount:   perf.HitStopLossCount,
		AverageChangePct:   perf.AverageChangePct,
		MedianChangePct:    perf.MedianChangePct,
		WinRatePct:         perf.WinRatePct,
		TakeProfitRatePct:  perf.TakeProfitRatePct,
		StopLossRatePct:    perf.StopLossRatePct,
		LastReviewedRunAt:  perf.LatestReviewedRunAt,
	}
	if perf.BestPick != nil {
		copyItem := *perf.BestPick
		summary.BestPick = &copyItem
	}
	if perf.WorstPick != nil {
		copyItem := *perf.WorstPick
		summary.WorstPick = &copyItem
	}
	if err := saveSummary(summaryPath, summary); err != nil {
		return nil, err
	}
	return summary, nil
}

func loadSummary(path string) (*Summary, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	var summary Summary
	if err := json.Unmarshal(data, &summary); err != nil {
		return nil, err
	}
	return &summary, nil
}

func saveSummary(path string, summary *Summary) error {
	if summary == nil {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(abs, data, 0o600)
}

func status(rec domain.Recommendation, current float64) string {
	switch {
	case current >= rec.TakeProfit:
		return "take_profit_hit"
	case current <= rec.StopLoss:
		return "stop_loss_hit"
	case current > rec.BuyPrice:
		return "above_buy"
	case current < rec.BuyPrice:
		return "below_buy"
	default:
		return "at_buy"
	}
}

func percentChange(start, current float64) float64 {
	if start == 0 {
		return 0
	}
	return ((current - start) / start) * 100
}

func buildSummary(review *Review) string {
	return "Reviewed " + itoa(review.EvaluatedCount) +
		" prior picks: " + itoa(review.AboveBuyCount) + " above buy, " +
		itoa(review.HitTakeProfitCount) + " hit take profit, " +
		itoa(review.HitStopLossCount) + " hit stop loss."
}

func itoa(value int) string {
	return strconv.Itoa(value)
}

func pct(part, total int) float64 {
	if total == 0 {
		return 0
	}
	return (float64(part) / float64(total)) * 100
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
