package analyzer

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"stonks/internal/config"
	"stonks/internal/domain"
	"stonks/internal/source"
)

type AnalysisMode string

const (
	ModeHybrid      AnalysisMode = "hybrid"
	ModeTechnical   AnalysisMode = "technical"
	ModeFloorsheet  AnalysisMode = "floorsheet"
	ModeFundamental AnalysisMode = "fundamental"
)

type Engine struct {
	cfg     config.Config
	sources []source.Source
}

func New(cfg config.Config, sources []source.Source) *Engine {
	return &Engine{cfg: cfg, sources: sources}
}

func ParseMode(value string) AnalysisMode {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "hybrid":
		return ModeHybrid
	case "technical":
		return ModeTechnical
	case "floorsheet":
		return ModeFloorsheet
	case "fundamental":
		return ModeFundamental
	default:
		return ModeHybrid
	}
}

func (e *Engine) Analyze(ctx context.Context, mode AnalysisMode) (domain.AnalysisResult, error) {
	return e.AnalyzeSymbol(ctx, mode, "")
}

func (e *Engine) AnalyzeSymbol(ctx context.Context, mode AnalysisMode, symbolFilter string) (domain.AnalysisResult, error) {
	recommendations := make([]domain.Recommendation, 0)
	discarded := make([]domain.Discarded, 0)
	fallbacks := make([]domain.Recommendation, 0)
	latestQuotes := make(map[string]float64)
	symbolFilter = strings.ToUpper(strings.TrimSpace(symbolFilter))

	for _, src := range e.sources {
		snapshot, err := src.Fetch(ctx)
		if err != nil {
			discarded = append(discarded, domain.Discarded{
				Symbol: src.Name(),
				Reason: err.Error(),
			})
			continue
		}
		if symbolFilter != "" {
			snapshot = filterSnapshot(snapshot, symbolFilter)
		}
		for _, quote := range snapshot.Quotes {
			if quote.Symbol != "" && quote.LastPrice > 0 {
				latestQuotes[quote.Symbol] = quote.LastPrice
			}
		}
		recs, rejected, sourceFallbacks := e.analyzeSnapshot(ctx, src, snapshot, mode)
		recommendations = append(recommendations, recs...)
		discarded = append(discarded, rejected...)
		fallbacks = append(fallbacks, sourceFallbacks...)
	}

	sortRecommendations(recommendations)
	sortDiscarded(discarded)
	if len(recommendations) > e.cfg.TopN {
		recommendations = recommendations[:e.cfg.TopN]
	}

	status := "ok"
	if len(recommendations) == 0 {
		sortRecommendations(fallbacks)
		if len(fallbacks) > e.cfg.TopN {
			fallbacks = fallbacks[:e.cfg.TopN]
		}
		if len(fallbacks) > 0 {
			recommendations = fallbacks
			status = "fallback_candidates"
		} else {
			status = "no_candidates"
		}
	}

	return domain.AnalysisResult{
		MarketStatus:      status,
		Mode:              string(mode),
		RecommendationSet: recommendations,
		DiscardedSymbols:  discarded,
		LatestQuotes:      latestQuotes,
		AnalyzedAt:        time.Now().UTC(),
	}, nil
}

func filterSnapshot(snapshot domain.MarketSnapshot, symbol string) domain.MarketSnapshot {
	filtered := domain.MarketSnapshot{
		Quotes: make([]domain.Quote, 0, len(snapshot.Quotes)),
		Trades: make([]domain.Trade, 0, len(snapshot.Trades)),
	}
	for _, quote := range snapshot.Quotes {
		if strings.EqualFold(strings.TrimSpace(quote.Symbol), symbol) {
			filtered.Quotes = append(filtered.Quotes, quote)
		}
	}
	for _, trade := range snapshot.Trades {
		if strings.EqualFold(strings.TrimSpace(trade.Symbol), symbol) {
			filtered.Trades = append(filtered.Trades, trade)
		}
	}
	return filtered
}

func sortRecommendations(items []domain.Recommendation) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Confidence == items[j].Confidence {
			return items[i].RiskReward > items[j].RiskReward
		}
		return items[i].Confidence > items[j].Confidence
	})
}

func (e *Engine) analyzeSnapshot(ctx context.Context, src source.Source, snapshot domain.MarketSnapshot, mode AnalysisMode) ([]domain.Recommendation, []domain.Discarded, []domain.Recommendation) {
	tradeStats := aggregateTrades(snapshot.Trades)
	recs := make([]domain.Recommendation, 0)
	discarded := make([]domain.Discarded, 0)
	fallbacks := make([]domain.Recommendation, 0)

	var candleProvider source.CandleProvider
	if provider, ok := src.(source.CandleProvider); ok {
		candleProvider = provider
	}
	var fundamentalProvider source.FundamentalProvider
	if provider, ok := src.(source.FundamentalProvider); ok {
		fundamentalProvider = provider
	}

	type candidate struct {
		quote    domain.Quote
		stats    tradeAggregate
		preScore float64
	}
	candidates := make([]candidate, 0)

	for _, quote := range snapshot.Quotes {
		symbol := strings.TrimSpace(quote.Symbol)
		if symbol == "" {
			continue
		}
		stats := tradeStats[symbol]
		rec, ok, reason := e.preScore(mode, src.Name(), quote, stats)
		if !ok {
			discarded = append(discarded, domain.Discarded{Symbol: symbol, Reason: reason})
			continue
		}
		candidates = append(candidates, candidate{
			quote:    quote,
			stats:    stats,
			preScore: rec.Confidence,
		})
		recs = append(recs, rec)
	}

	if len(candidates) == 0 {
		return recs, discarded, fallbacks
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].preScore > candidates[j].preScore
	})
	limit := e.cfg.CandleShortlistSize
	if limit <= 0 || limit > len(candidates) {
		limit = len(candidates)
	}

	recs = make([]domain.Recommendation, 0, limit)
	for _, item := range candidates[:limit] {
		var candles []domain.Candle
		if candleProvider != nil {
			var err error
			candles, err = candleProvider.FetchCandles(ctx, item.quote.Symbol, e.cfg.HistoryResolution, e.cfg.HistoryFrame)
			if err != nil {
				discarded = append(discarded, domain.Discarded{
					Symbol: item.quote.Symbol,
					Reason: "candle fetch: " + err.Error(),
				})
			}
		}
		if candleProvider != nil {
			if ok, reason := e.activeByCandles(candles, time.Now().UTC()); !ok {
				discarded = append(discarded, domain.Discarded{
					Symbol: item.quote.Symbol,
					Reason: reason,
				})
				continue
			}
		}

		var fundamentals domain.Fundamentals
		if fundamentalProvider != nil && (mode == ModeHybrid || mode == ModeFundamental) {
			var err error
			fundamentals, err = fundamentalProvider.FetchFundamentals(ctx, item.quote.Symbol)
			if err != nil {
				discarded = append(discarded, domain.Discarded{
					Symbol: item.quote.Symbol,
					Reason: "fundamental fetch: " + err.Error(),
				})
			}
		}

		rec, ok, reason := e.score(mode, src.Name(), item.quote, item.stats, candles, fundamentals)
		if !ok {
			discarded = append(discarded, domain.Discarded{Symbol: item.quote.Symbol, Reason: reason})
			if fallback, fallbackOK := e.fallbackScore(mode, src.Name(), item.quote, item.stats, candles, fundamentals); fallbackOK {
				fallbacks = append(fallbacks, fallback)
			}
			continue
		}
		recs = append(recs, rec)
	}
	return recs, discarded, fallbacks
}

func (e *Engine) activeByCandles(candles []domain.Candle, now time.Time) (bool, string) {
	if len(candles) == 0 {
		return false, "inactive ticker: no candle history available"
	}
	latest := candles[len(candles)-1].Timestamp
	if latest.IsZero() {
		return false, "inactive ticker: missing latest trade date"
	}
	lookbackDays := e.cfg.InactiveLookbackDays
	if lookbackDays <= 0 {
		lookbackDays = 120
	}
	if latest.Before(now.AddDate(0, 0, -lookbackDays)) {
		return false, fmt.Sprintf("inactive ticker: latest trading day %s older than %d days", latest.Format("2006-01-02"), lookbackDays)
	}

	minTradeDays := e.cfg.InactiveMinTradeDays
	if minTradeDays <= 0 {
		minTradeDays = 5
	}
	activeDays := 0
	window := 60
	if window > len(candles) {
		window = len(candles)
	}
	for _, candle := range candles[len(candles)-window:] {
		if candle.Volume > 0 {
			activeDays++
		}
	}
	if activeDays < minTradeDays {
		return false, fmt.Sprintf("inactive ticker: only %d active trading days in last %d sessions", activeDays, window)
	}
	return true, ""
}

func (e *Engine) preScore(mode AnalysisMode, sourceName string, quote domain.Quote, stats tradeAggregate) (domain.Recommendation, bool, string) {
	last := quote.LastPrice
	if last <= 0 {
		return domain.Recommendation{}, false, "missing last traded price"
	}
	minVolume := minVolumeForMode(e.cfg, mode)
	if requiresVolume(mode) && quote.Volume < minVolume {
		return domain.Recommendation{}, false, fmt.Sprintf("volume %.0f below minimum %.0f", quote.Volume, minVolume)
	}

	dayRange := positiveOrDefault(quote.HighPrice-quote.LowPrice, last*0.03)
	momentumScore := clamp01(((last-quote.OpenPrice)/positiveOrDefault(quote.OpenPrice, last))*2 + ((last-quote.PrevClose)/positiveOrDefault(quote.PrevClose, last))*2 + (quote.HighPrice-last)/positiveOrDefault(dayRange, 1))
	liquidityScore := clamp01(math.Log10(quote.Volume+1) / 7)
	floorsheetScore := scoreFloorsheet(stats)

	confidence := round4(weightedConfidence(mode, momentumScore, liquidityScore, floorsheetScore, 0.5, 0.5))
	return domain.Recommendation{
		Symbol:           quote.Symbol,
		Source:           sourceName,
		Mode:             string(mode),
		Verdict:          "watchlist",
		BuyPrice:         round2(last),
		MomentumScore:    round4(momentumScore),
		LiquidityScore:   round4(liquidityScore),
		FloorsheetScore:  round4(floorsheetScore),
		IndicatorScore:   0.5,
		FundamentalScore: 0.5,
		Confidence:       confidence,
		GeneratedAt:      time.Now().UTC(),
	}, true, ""
}

func (e *Engine) score(mode AnalysisMode, sourceName string, quote domain.Quote, stats tradeAggregate, candles []domain.Candle, fundamentals domain.Fundamentals) (domain.Recommendation, bool, string) {
	last := quote.LastPrice
	if last <= 0 {
		return domain.Recommendation{}, false, "missing last traded price"
	}
	minVolume := minVolumeForMode(e.cfg, mode)
	if requiresVolume(mode) && quote.Volume < minVolume {
		return domain.Recommendation{}, false, fmt.Sprintf("volume %.0f below minimum %.0f", quote.Volume, minVolume)
	}
	if requiresFloorsheet(mode) && stats.Count < e.cfg.MinTradeCount {
		return domain.Recommendation{}, false, fmt.Sprintf("floorsheet trades %d below minimum %d", stats.Count, e.cfg.MinTradeCount)
	}
	if mode == ModeFundamental && !fundamentals.Loaded {
		return domain.Recommendation{}, false, "fundamentals unavailable"
	}
	if mode == ModeTechnical && len(candles) < 50 {
		return domain.Recommendation{}, false, "technical indicators unavailable"
	}
	if mode == ModeHybrid {
		switch {
		case !fundamentals.Loaded && len(candles) < 50:
			return domain.Recommendation{}, false, "hybrid data unavailable"
		case !fundamentals.Loaded:
			return domain.Recommendation{}, false, "fundamentals unavailable"
		case len(candles) < 50:
			return domain.Recommendation{}, false, "technical indicators unavailable"
		}
	}

	dayRange := positiveOrDefault(quote.HighPrice-quote.LowPrice, last*0.03)
	momentumScore := clamp01(((last-quote.OpenPrice)/positiveOrDefault(quote.OpenPrice, last))*2 + ((last-quote.PrevClose)/positiveOrDefault(quote.PrevClose, last))*2 + (quote.HighPrice-last)/positiveOrDefault(dayRange, 1))
	liquidityScore := clamp01(math.Log10(quote.Volume+1) / 7)
	floorsheetScore := scoreFloorsheet(stats)

	indicatorScore, indicators := scoreIndicators(candles, last)
	fundamentalScore := 0.5
	if fundamentals.Loaded {
		fundamentalScore = clamp01(fundamentals.FundamentalScore)
	}

	atrRisk := dayRange * 0.7
	if indicators.ATR14 > 0 {
		atrRisk = indicators.ATR14 * 1.2
	}
	stopLoss := round2(max(last-atrRisk, last*(1-e.cfg.MaxRiskPerTradePct/100)))
	if stopLoss >= last {
		stopLoss = round2(last * 0.98)
	}
	risk := last - stopLoss
	if risk <= 0 {
		return domain.Recommendation{}, false, "non-positive risk window"
	}
	takeProfit := round2(targetPrice(last, risk, floorsheetScore, indicatorScore, indicators, candles))
	riskReward := (takeProfit - last) / risk
	if riskReward < e.cfg.MinRiskReward {
		return domain.Recommendation{}, false, fmt.Sprintf("risk/reward %.2f below threshold %.2f", riskReward, e.cfg.MinRiskReward)
	}

	confidence := round4(weightedConfidence(mode, momentumScore, liquidityScore, floorsheetScore, indicatorScore, fundamentalScore))
	reasoning := buildReasoning(mode, quote, stats, candles, fundamentals, indicators, confidence)
	verdict := recommendationVerdict(mode, confidence, riskReward, fundamentals.Loaded, len(candles) >= 50, false)
	if verdict == "avoid" {
		return domain.Recommendation{}, false, "underwhelming setup"
	}

	return domain.Recommendation{
		Symbol:             quote.Symbol,
		Source:             sourceName,
		Mode:               string(mode),
		Verdict:            verdict,
		BuyPrice:           round2(last),
		StopLoss:           stopLoss,
		TakeProfit:         takeProfit,
		RiskReward:         round2(riskReward),
		RiskRewardRatio:    riskRewardRatio(riskReward),
		MomentumScore:      round4(momentumScore),
		LiquidityScore:     round4(liquidityScore),
		FloorsheetScore:    round4(floorsheetScore),
		IndicatorScore:     round4(indicatorScore),
		FundamentalScore:   round4(fundamentalScore),
		RSI14:              round2(indicators.RSI14),
		EMA20:              round2(indicators.EMA20),
		EMA50:              round2(indicators.EMA50),
		ATR14:              round2(indicators.ATR14),
		PE:                 round2(fundamentals.PE),
		PB:                 round2(fundamentals.PB),
		ROE:                round2(fundamentals.ROE),
		EPS:                round2(nonZeroOrFallback(fundamentals.EPSTTM, fundamentals.EPS)),
		BVPS:               round2(fundamentals.BVPS),
		IndicatorsLoaded:   len(candles) >= 50,
		FundamentalsLoaded: fundamentals.Loaded,
		Confidence:         confidence,
		Reasoning:          reasoning,
		GeneratedAt:        time.Now().UTC(),
	}, true, ""
}

func (e *Engine) fallbackScore(mode AnalysisMode, sourceName string, quote domain.Quote, stats tradeAggregate, candles []domain.Candle, fundamentals domain.Fundamentals) (domain.Recommendation, bool) {
	last := quote.LastPrice
	if last <= 0 {
		return domain.Recommendation{}, false
	}
	if mode == ModeTechnical && len(candles) < 50 {
		return domain.Recommendation{}, false
	}
	if mode == ModeFundamental && !fundamentals.Loaded {
		return domain.Recommendation{}, false
	}
	if mode == ModeHybrid && (!fundamentals.Loaded || len(candles) < 50) {
		return domain.Recommendation{}, false
	}

	dayRange := positiveOrDefault(quote.HighPrice-quote.LowPrice, last*0.03)
	momentumScore := clamp01(((last-quote.OpenPrice)/positiveOrDefault(quote.OpenPrice, last))*2 + ((last-quote.PrevClose)/positiveOrDefault(quote.PrevClose, last))*2 + (quote.HighPrice-last)/positiveOrDefault(dayRange, 1))
	liquidityScore := clamp01(math.Log10(quote.Volume+1) / 7)
	floorsheetScore := scoreFloorsheet(stats)
	indicatorScore, indicators := scoreIndicators(candles, last)
	fundamentalScore := 0.5
	if fundamentals.Loaded {
		fundamentalScore = clamp01(fundamentals.FundamentalScore)
	}

	stopLoss := round2(last - max(dayRange*0.8, last*0.02))
	if stopLoss >= last {
		stopLoss = round2(last * 0.98)
	}
	risk := last - stopLoss
	if risk <= 0 {
		return domain.Recommendation{}, false
	}

	confidence := round4(weightedConfidence(mode, momentumScore, liquidityScore, floorsheetScore, indicatorScore, fundamentalScore))
	takeProfit := round2(targetPrice(last, risk, floorsheetScore, indicatorScore, indicators, candles))
	riskReward := round2((takeProfit - last) / risk)
	verdict := recommendationVerdict(mode, confidence, riskReward, fundamentals.Loaded, len(candles) >= 50, true)
	if verdict == "avoid" {
		return domain.Recommendation{}, false
	}

	reasoning := []string{
		"Returned as the best available setup after strict filters rejected all symbols.",
		fmt.Sprintf("volume %.0f, floorsheet trades %d, confidence %.2f", quote.Volume, stats.Count, confidence),
	}
	if stats.Count > 0 {
		reasoning = append(reasoning, fmt.Sprintf("top buyer %s %.0f vs top seller %s %.0f, accumulation %.0f%%", stats.TopBuyer, stats.TopBuyerQty, stats.TopSeller, stats.TopSellerQty, round2(stats.AccumulationBias*100)))
	}
	if len(candles) >= 50 {
		reasoning = append(reasoning, fmt.Sprintf("EMA20 %.2f vs EMA50 %.2f with RSI14 %.2f", indicators.EMA20, indicators.EMA50, indicators.RSI14))
	}
	if fundamentals.Loaded {
		reasoning = append(reasoning, fmt.Sprintf("PE %.2f, PB %.2f, ROE %.2f, EPS %.2f", fundamentals.PE, fundamentals.PB, fundamentals.ROE, nonZeroOrFallback(fundamentals.EPSTTM, fundamentals.EPS)))
	}

	return domain.Recommendation{
		Symbol:             quote.Symbol,
		Source:             sourceName,
		Mode:               string(mode),
		Verdict:            verdict,
		BuyPrice:           round2(last),
		StopLoss:           stopLoss,
		TakeProfit:         takeProfit,
		RiskReward:         riskReward,
		RiskRewardRatio:    riskRewardRatio(riskReward),
		MomentumScore:      round4(momentumScore),
		LiquidityScore:     round4(liquidityScore),
		FloorsheetScore:    round4(floorsheetScore),
		IndicatorScore:     round4(indicatorScore),
		FundamentalScore:   round4(fundamentalScore),
		RSI14:              round2(indicators.RSI14),
		EMA20:              round2(indicators.EMA20),
		EMA50:              round2(indicators.EMA50),
		ATR14:              round2(indicators.ATR14),
		PE:                 round2(fundamentals.PE),
		PB:                 round2(fundamentals.PB),
		ROE:                round2(fundamentals.ROE),
		EPS:                round2(nonZeroOrFallback(fundamentals.EPSTTM, fundamentals.EPS)),
		BVPS:               round2(fundamentals.BVPS),
		IndicatorsLoaded:   len(candles) >= 50,
		FundamentalsLoaded: fundamentals.Loaded,
		Confidence:         confidence,
		Reasoning:          reasoning,
		GeneratedAt:        time.Now().UTC(),
	}, true
}

func recommendationVerdict(mode AnalysisMode, confidence, riskReward float64, fundamentalsLoaded, indicatorsLoaded, fallback bool) string {
	switch mode {
	case ModeFundamental:
		if confidence >= 0.72 && fundamentalsLoaded {
			return "buy"
		}
		if confidence >= 0.52 && fundamentalsLoaded {
			return "watchlist"
		}
		return "avoid"
	case ModeTechnical:
		if indicatorsLoaded && confidence >= 0.74 && riskReward >= 2.0 && !fallback {
			return "buy"
		}
		if indicatorsLoaded && confidence >= 0.54 && riskReward >= 1.5 {
			return "watchlist"
		}
		return "avoid"
	case ModeFloorsheet:
		if confidence >= 0.76 && riskReward >= 2.0 && !fallback {
			return "buy"
		}
		if confidence >= 0.50 && riskReward >= 1.3 {
			return "watchlist"
		}
		return "avoid"
	default:
		if fundamentalsLoaded && indicatorsLoaded && confidence >= 0.74 && riskReward >= 2.0 && !fallback {
			return "buy"
		}
		if confidence >= 0.54 && riskReward >= 1.5 {
			return "watchlist"
		}
		return "avoid"
	}
}

func buildReasoning(mode AnalysisMode, quote domain.Quote, stats tradeAggregate, candles []domain.Candle, fundamentals domain.Fundamentals, indicators indicatorSet, confidence float64) []string {
	reasoning := make([]string, 0, 5)
	reasoning = append(reasoning, fmt.Sprintf("mode %s with confidence %.2f", mode, confidence))
	reasoning = append(reasoning, fmt.Sprintf("price %.2f with day range %.2f", quote.LastPrice, positiveOrDefault(quote.HighPrice-quote.LowPrice, quote.LastPrice*0.03)))

	if mode != ModeFundamental {
		reasoning = append(reasoning, fmt.Sprintf("volume %.0f and turnover %.2f indicate liquidity", quote.Volume, quote.Turnover))
	}
	if mode == ModeHybrid || mode == ModeFloorsheet {
		reasoning = append(reasoning, fmt.Sprintf("floorsheet trade count %d with %.0f quantity lifting above average price", stats.Count, stats.AboveAverageQty))
	}
	if len(candles) >= 50 && (mode == ModeHybrid || mode == ModeTechnical) {
		reasoning = append(reasoning, fmt.Sprintf("EMA20 %.2f vs EMA50 %.2f with RSI14 %.2f", indicators.EMA20, indicators.EMA50, indicators.RSI14))
	}
	if fundamentals.Loaded && (mode == ModeHybrid || mode == ModeFundamental) {
		reasoning = append(reasoning, fmt.Sprintf("PE %.2f, PB %.2f, ROE %.2f, EPS %.2f", fundamentals.PE, fundamentals.PB, fundamentals.ROE, nonZeroOrFallback(fundamentals.EPSTTM, fundamentals.EPS)))
	}
	if stats.Count > 0 && (mode == ModeHybrid || mode == ModeFloorsheet) {
		reasoning = append(reasoning, fmt.Sprintf("top buyer %s %.0f vs top seller %s %.0f, accumulation %.0f%%", stats.TopBuyer, stats.TopBuyerQty, stats.TopSeller, stats.TopSellerQty, round2(stats.AccumulationBias*100)))
	}
	return reasoning
}

func requiresVolume(mode AnalysisMode) bool {
	return true
}

func minVolumeForMode(cfg config.Config, mode AnalysisMode) float64 {
	switch mode {
	case ModeFundamental:
		return 0
	case ModeTechnical, ModeFloorsheet:
		return 5000
	default:
		return max(cfg.MinQuoteVolume, 2000)
	}
}

func requiresFloorsheet(mode AnalysisMode) bool {
	return mode == ModeHybrid || mode == ModeFloorsheet
}

func weightedConfidence(mode AnalysisMode, momentumScore, liquidityScore, floorsheetScore, indicatorScore, fundamentalScore float64) float64 {
	switch mode {
	case ModeTechnical:
		return momentumScore*0.35 + liquidityScore*0.15 + indicatorScore*0.5
	case ModeFloorsheet:
		return momentumScore*0.15 + liquidityScore*0.25 + floorsheetScore*0.6
	case ModeFundamental:
		return liquidityScore*0.15 + momentumScore*0.1 + fundamentalScore*0.75
	default:
		return momentumScore*0.2 + liquidityScore*0.15 + floorsheetScore*0.25 + indicatorScore*0.2 + fundamentalScore*0.2
	}
}

func sortDiscarded(items []domain.Discarded) {
	sort.SliceStable(items, func(i, j int) bool {
		pi := discardPriority(items[i].Reason)
		pj := discardPriority(items[j].Reason)
		if pi == pj {
			return items[i].Symbol < items[j].Symbol
		}
		return pi < pj
	})
}

func discardPriority(reason string) int {
	lower := strings.ToLower(reason)
	switch {
	case strings.Contains(lower, "fetch"):
		return 0
	case strings.Contains(lower, "technical indicators unavailable"):
		return 1
	case strings.Contains(lower, "fundamentals unavailable"):
		return 2
	case strings.Contains(lower, "hybrid data unavailable"):
		return 3
	case strings.Contains(lower, "risk/reward"):
		return 4
	case strings.Contains(lower, "floorsheet"):
		return 5
	case strings.Contains(lower, "volume"):
		return 6
	default:
		return 7
	}
}

func nonZeroOrFallback(primary, fallback float64) float64 {
	if primary != 0 {
		return primary
	}
	return fallback
}

type tradeAggregate struct {
	Count               int
	TotalQty            float64
	AveragePrice        float64
	AboveAverageQty     float64
	TopBuyer            string
	TopBuyerQty         float64
	TopBuyerTrades      int
	TopSeller           string
	TopSellerQty        float64
	TopSellerTrades     int
	UniqueBuyers        int
	UniqueSellers       int
	UptickQty           float64
	DowntickQty         float64
	LongestUptickRun    int
	BuyerPersistence    float64
	SellerPersistence   float64
	BrokerNetBias       float64
	AccumulationBias    float64
	BrokerConcentration float64
}

func aggregateTrades(trades []domain.Trade) map[string]tradeAggregate {
	grouped := make(map[string][]domain.Trade)
	for _, trade := range trades {
		grouped[trade.Symbol] = append(grouped[trade.Symbol], trade)
	}

	out := make(map[string]tradeAggregate, len(grouped))
	for symbol, items := range grouped {
		agg := tradeAggregate{Count: len(items)}
		buyerQty := make(map[string]float64)
		sellerQty := make(map[string]float64)
		buyerTrades := make(map[string]int)
		sellerTrades := make(map[string]int)
		totalValue := 0.0

		for _, trade := range items {
			agg.TotalQty += trade.Quantity
			totalValue += trade.Quantity * trade.Price
			if broker := strings.TrimSpace(trade.BuyerBroker); broker != "" {
				buyerQty[broker] += trade.Quantity
				buyerTrades[broker]++
			}
			if broker := strings.TrimSpace(trade.SellerBroker); broker != "" {
				sellerQty[broker] += trade.Quantity
				sellerTrades[broker]++
			}
		}
		if agg.TotalQty > 0 {
			agg.AveragePrice = totalValue / agg.TotalQty
		}

		uptickRun := 0
		prevPrice := 0.0
		for _, trade := range items {
			if trade.Price >= agg.AveragePrice {
				agg.AboveAverageQty += trade.Quantity
			}
			switch {
			case prevPrice == 0 || trade.Price > prevPrice:
				agg.UptickQty += trade.Quantity
				uptickRun++
				if uptickRun > agg.LongestUptickRun {
					agg.LongestUptickRun = uptickRun
				}
			case trade.Price < prevPrice:
				agg.DowntickQty += trade.Quantity
				uptickRun = 0
			default:
				agg.UptickQty += trade.Quantity * 0.5
				agg.DowntickQty += trade.Quantity * 0.5
			}
			prevPrice = trade.Price
		}

		agg.UniqueBuyers = len(buyerQty)
		agg.UniqueSellers = len(sellerQty)
		agg.TopBuyer, agg.TopBuyerQty, agg.TopBuyerTrades = topBroker(buyerQty, buyerTrades)
		agg.TopSeller, agg.TopSellerQty, agg.TopSellerTrades = topBroker(sellerQty, sellerTrades)
		if agg.TotalQty > 0 {
			agg.BrokerConcentration = max(agg.TopBuyerQty, agg.TopSellerQty) / agg.TotalQty
			agg.BuyerPersistence = float64(agg.TopBuyerTrades) / float64(maxInt(agg.Count, 1))
			agg.SellerPersistence = float64(agg.TopSellerTrades) / float64(maxInt(agg.Count, 1))
			agg.BrokerNetBias = (agg.TopBuyerQty - agg.TopSellerQty) / agg.TotalQty
			agg.AccumulationBias = (agg.UptickQty - agg.DowntickQty) / agg.TotalQty
		}
		out[symbol] = agg
	}
	return out
}

func clamp01(value float64) float64 {
	switch {
	case value < 0:
		return 0
	case value > 1:
		return 1
	default:
		return value
	}
}

func positiveOrDefault(value, fallback float64) float64 {
	if value > 0 {
		return value
	}
	return fallback
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func round2(value float64) float64 {
	return math.Round(value*100) / 100
}

func round4(value float64) float64 {
	return math.Round(value*10000) / 10000
}

func riskRewardRatio(riskReward float64) string {
	return fmt.Sprintf("1:%.2f", round2(riskReward))
}

func topBroker(qty map[string]float64, trades map[string]int) (string, float64, int) {
	bestBroker := ""
	bestQty := 0.0
	bestTrades := 0
	for broker, value := range qty {
		if value > bestQty {
			bestBroker = broker
			bestQty = value
			bestTrades = trades[broker]
		}
	}
	return bestBroker, bestQty, bestTrades
}

func scoreFloorsheet(stats tradeAggregate) float64 {
	if stats.Count == 0 || stats.TotalQty <= 0 {
		return 0.35
	}
	flowBias := clamp01(stats.AboveAverageQty / stats.TotalQty)
	activityScore := clamp01(float64(stats.Count) / 80)
	brokerBias := clamp01(0.5 + stats.BrokerNetBias*1.5)
	persistenceScore := clamp01((stats.BuyerPersistence * 0.7) + ((1 - stats.SellerPersistence) * 0.3))
	accumulationScore := clamp01(0.5 + stats.AccumulationBias*1.8 + float64(stats.LongestUptickRun)/20)
	concentrationScore := clamp01(0.5 + stats.BrokerConcentration*0.6)
	return round4(flowBias*0.22 + activityScore*0.14 + brokerBias*0.22 + persistenceScore*0.16 + accumulationScore*0.18 + concentrationScore*0.08)
}

func targetPrice(last, risk, floorsheetScore, indicatorScore float64, indicators indicatorSet, candles []domain.Candle) float64 {
	momentumRR := 1.6 + indicatorScore*0.9 + floorsheetScore*0.7
	structuralTarget := last + risk*momentumRR

	atrTarget := structuralTarget
	if indicators.ATR14 > 0 {
		atrTarget = max(atrTarget, last+indicators.ATR14*(1.8+indicatorScore*1.4+floorsheetScore*0.6))
	}

	resistanceTarget := 0.0
	if len(candles) >= 20 {
		recentHigh := recentResistance(candles, 20)
		switch {
		case recentHigh > last*1.01:
			resistanceTarget = recentHigh
		case recentHigh > 0:
			resistanceTarget = last + (recentHigh-last)*0.5 + risk*(0.8+indicatorScore*0.6)
		}
	}

	target := max(structuralTarget, max(atrTarget, resistanceTarget))
	minTarget := last + risk*1.55
	maxTarget := last + risk*3.6
	if target < minTarget {
		target = minTarget
	}
	if target > maxTarget {
		target = maxTarget
	}
	return target
}

func recentResistance(candles []domain.Candle, window int) float64 {
	if len(candles) == 0 {
		return 0
	}
	if window > len(candles) {
		window = len(candles)
	}
	start := len(candles) - window
	high := 0.0
	for _, candle := range candles[start:] {
		if candle.High > high {
			high = candle.High
		}
	}
	return high
}

type indicatorSet struct {
	RSI14 float64
	EMA20 float64
	EMA50 float64
	ATR14 float64
}

func scoreIndicators(candles []domain.Candle, last float64) (float64, indicatorSet) {
	if len(candles) < 50 {
		return 0.5, indicatorSet{}
	}

	closes := make([]float64, 0, len(candles))
	for _, candle := range candles {
		closes = append(closes, candle.Close)
	}

	ema20 := ema(closes, 20)
	ema50 := ema(closes, 50)
	rsi14 := rsi(closes, 14)
	atr14 := atr(candles, 14)

	trendScore := 0.0
	if ema20 > 0 && ema50 > 0 {
		trendScore = clamp01(0.5 + ((ema20-ema50)/ema50)*8)
	}
	priceVsEMA := 0.0
	if ema20 > 0 {
		priceVsEMA = clamp01(0.5 + ((last-ema20)/ema20)*10)
	}
	rsiScore := 0.5
	if rsi14 > 0 {
		switch {
		case rsi14 >= 50 && rsi14 <= 68:
			rsiScore = 1
		case rsi14 > 68 && rsi14 <= 75:
			rsiScore = 0.7
		case rsi14 >= 40 && rsi14 < 50:
			rsiScore = 0.55
		default:
			rsiScore = 0.3
		}
	}

	score := round4(trendScore*0.45 + priceVsEMA*0.25 + rsiScore*0.3)
	return score, indicatorSet{
		RSI14: rsi14,
		EMA20: ema20,
		EMA50: ema50,
		ATR14: atr14,
	}
}

func ema(values []float64, period int) float64 {
	if len(values) < period || period <= 0 {
		return 0
	}
	multiplier := 2.0 / float64(period+1)
	emaValue := average(values[:period])
	for _, value := range values[period:] {
		emaValue = ((value - emaValue) * multiplier) + emaValue
	}
	return emaValue
}

func rsi(values []float64, period int) float64 {
	if len(values) <= period {
		return 0
	}
	gain := 0.0
	loss := 0.0
	for i := 1; i <= period; i++ {
		change := values[i] - values[i-1]
		if change > 0 {
			gain += change
		} else {
			loss -= change
		}
	}
	avgGain := gain / float64(period)
	avgLoss := loss / float64(period)
	for i := period + 1; i < len(values); i++ {
		change := values[i] - values[i-1]
		currentGain := 0.0
		currentLoss := 0.0
		if change > 0 {
			currentGain = change
		} else {
			currentLoss = -change
		}
		avgGain = ((avgGain * float64(period-1)) + currentGain) / float64(period)
		avgLoss = ((avgLoss * float64(period-1)) + currentLoss) / float64(period)
	}
	if avgLoss == 0 {
		return 100
	}
	rs := avgGain / avgLoss
	return 100 - (100 / (1 + rs))
}

func atr(candles []domain.Candle, period int) float64 {
	if len(candles) <= period {
		return 0
	}
	trs := make([]float64, 0, len(candles)-1)
	for i := 1; i < len(candles); i++ {
		current := candles[i]
		prevClose := candles[i-1].Close
		tr := math.Max(current.High-current.Low, math.Max(math.Abs(current.High-prevClose), math.Abs(current.Low-prevClose)))
		trs = append(trs, tr)
	}
	if len(trs) < period {
		return 0
	}
	atrValue := average(trs[:period])
	for _, tr := range trs[period:] {
		atrValue = ((atrValue * float64(period-1)) + tr) / float64(period)
	}
	return atrValue
}

func average(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, value := range values {
		sum += value
	}
	return sum / float64(len(values))
}
