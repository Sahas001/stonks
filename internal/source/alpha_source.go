package source

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"stonks/internal/domain"
)

type AlphaSourceConfig struct {
	Base HTTPSourceConfig

	ForceURLKeyPath string
	ForceURLKeyFS   string
	HistoryPath     string
	ProfilePath     string
}

type AlphaSource struct {
	base            *HTTPSource
	forceURLKeyPath string
	forceURLKeyFS   string
	historyPath     string
	profilePath     string
}

func NewAlphaSource(cfg AlphaSourceConfig) *AlphaSource {
	baseCfg := cfg.Base
	baseCfg.BaseURL = normalizeAlphaBaseURL(baseCfg.BaseURL)

	return &AlphaSource{
		base:            NewHTTPSource(baseCfg),
		forceURLKeyPath: cfg.ForceURLKeyPath,
		forceURLKeyFS:   cfg.ForceURLKeyFS,
		historyPath:     cfg.HistoryPath,
		profilePath:     cfg.ProfilePath,
	}
}

func (s *AlphaSource) Name() string {
	return s.base.Name()
}

func (s *AlphaSource) Fetch(ctx context.Context) (domain.MarketSnapshot, error) {
	boot, err := s.bootstrap(ctx)
	if err != nil {
		return domain.MarketSnapshot{}, err
	}

	prevQuotePath := s.base.quotePath
	prevFloorPath := s.base.floorPath
	s.base.quotePath = applyTokens(prevQuotePath, boot)
	s.base.floorPath = applyTokens(prevFloorPath, boot)
	defer func() {
		s.base.quotePath = prevQuotePath
		s.base.floorPath = prevFloorPath
	}()

	return s.base.Fetch(ctx)
}

func (s *AlphaSource) FetchCandles(ctx context.Context, symbol, resolution string, frame int) ([]domain.Candle, error) {
	boot, err := s.chartBootstrap(ctx, symbol)
	if err != nil {
		return nil, err
	}
	path := applyTokens(s.historyPath, boot)
	path = strings.ReplaceAll(path, "{{symbol}}", symbol)
	path = strings.ReplaceAll(path, "{{resolution}}", resolution)
	path = strings.ReplaceAll(path, "{{frame}}", strconv.Itoa(frame))

	payload, err := s.base.getWithOptions(
		ctx,
		path,
		"application/json, text/plain, */*",
		s.base.baseURL+"/trading/chart?symbol="+symbol,
		map[string]string{"X-Requested-With": "XMLHttpRequest"},
	)
	if err != nil {
		return nil, fmt.Errorf("alpha candle fetch: %w", err)
	}

	var raw struct {
		S string    `json:"s"`
		O []float64 `json:"o"`
		H []float64 `json:"h"`
		L []float64 `json:"l"`
		C []float64 `json:"c"`
		V []float64 `json:"v"`
		T []int64   `json:"t"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, fmt.Errorf("alpha candle decode: %w", err)
	}
	if raw.S != "ok" {
		return nil, fmt.Errorf("alpha candle status: %s", raw.S)
	}

	size := minLen(len(raw.O), len(raw.H), len(raw.L), len(raw.C), len(raw.V), len(raw.T))
	candles := make([]domain.Candle, 0, size)
	for i := 0; i < size; i++ {
		candles = append(candles, domain.Candle{
			Symbol:    symbol,
			Open:      raw.O[i],
			High:      raw.H[i],
			Low:       raw.L[i],
			Close:     raw.C[i],
			Volume:    raw.V[i],
			Timestamp: time.Unix(raw.T[i], 0).UTC(),
		})
	}
	return candles, nil
}

func (s *AlphaSource) FetchFundamentals(ctx context.Context, symbol string) (domain.Fundamentals, error) {
	path := strings.ReplaceAll(s.profilePath, "{{symbol}}", symbol)
	payload, err := s.base.getWithAccept(ctx, path, "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	if err != nil {
		return domain.Fundamentals{}, fmt.Errorf("alpha fundamentals fetch: %w", err)
	}

	props, err := extractDataPageProps(payload)
	if err != nil {
		return domain.Fundamentals{}, fmt.Errorf("alpha fundamentals parse: %w", err)
	}

	stockInfo := nestedMap(props, "stock_info")
	fundaTable := nestedMap(props, "funda_table")
	if len(fundaTable) == 0 {
		fundaTable = nestedMap(stockInfo, "funda_table")
	}
	generalInfo := nestedMap(props, "stocksGenralInfo")
	if len(generalInfo) == 0 {
		generalInfo = nestedMap(stockInfo, "general_info")
	}
	financialsEPS := nestedMap(props, "financialsEPS")
	quartesGrowths := collectRows(props["quartesGrowths"])
	pepbAvg := nestedMap(props, "pe_pb_avg")

	latestQuarter := latestQuarterMetrics(quartesGrowths)

	fundamentals := domain.Fundamentals{
		Symbol:          symbol,
		CompanyName:     strField(stockInfo, "full_name"),
		Sector:          strField(stockInfo, "formatted_sector", "sector"),
		PE:              numField(fundaTable, "pe_ratio"),
		PB:              numField(fundaTable, "pb_ratio"),
		PEG:             numField(fundaTable, "peg_ratio"),
		ROE:             normalizePercentLike(numField(fundaTable, "roe")),
		ROA:             normalizePercentLike(numField(fundaTable, "roa")),
		EPS:             numField(financialsEPS, "value"),
		EPSTTM:          numField(latestQuarter, "eps_ttm"),
		BVPS:            normalizeBVPS(numField(latestQuarter, "bvps"), numField(fundaTable, "ltp"), numField(fundaTable, "pb_ratio")),
		RevenueTTMYOY:   numField(latestQuarter, "revenuettm_yoy_growth"),
		NetProfitTTMYOY: numField(latestQuarter, "netprofitttmqtrl_yoy_growth"),
		DividendYield:   numField(fundaTable, "total_dividend_to_ltp"),
		PromoterHolding: numField(generalInfo, "promoter_holding"),
		PublicHolding:   numField(generalInfo, "public_holding"),
		FinancialDate:   timeField(latestQuarter, "financial_date"),
		Loaded:          true,
	}
	fundamentals.FundamentalScore = scoreFundamentals(fundamentals, numField(pepbAvg, "pe_avg"), numField(pepbAvg, "pb_avg"))
	return fundamentals, nil
}

type alphaBootstrap struct {
	FSK     string
	QuoteFS string
	LVS     string
}

func (s *AlphaSource) bootstrap(ctx context.Context) (alphaBootstrap, error) {
	path := s.forceURLKeyPath
	if path == "" {
		return alphaBootstrap{}, fmt.Errorf("alpha force-url-key path is empty")
	}

	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}

	payload, err := s.base.get(ctx, fmt.Sprintf("%s%sfs=%s&withAuth=1", path, separator, s.forceURLKeyFS))
	if err != nil {
		return alphaBootstrap{}, fmt.Errorf("alpha bootstrap fetch: %w", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return alphaBootstrap{}, fmt.Errorf("alpha bootstrap decode: %w", err)
	}

	fsk := strField(raw, "fsk", "key", "token")
	if fsk == "" {
		fsk = "1774599378987"
	}

	lvs := strField(raw, "fs", "lvs", "live_value", "live_session")
	if lvs == "" {
		lvs = s.forceURLKeyFS
	}

	return alphaBootstrap{
		FSK:     fsk,
		QuoteFS: quoteFSFromLVS(lvs),
		LVS:     lvs,
	}, nil
}

func (s *AlphaSource) chartBootstrap(ctx context.Context, symbol string) (alphaBootstrap, error) {
	randomFS := strconv.FormatFloat(rand.Float64(), 'f', 16, 64)
	payload, err := s.base.getWithOptions(
		ctx,
		"/force-url-key?fs="+randomFS,
		"*/*",
		s.base.baseURL+"/trading/chart?symbol="+symbol,
		nil,
	)
	if err != nil {
		return alphaBootstrap{}, fmt.Errorf("alpha chart bootstrap fetch: %w", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return alphaBootstrap{}, fmt.Errorf("alpha chart bootstrap decode: %w", err)
	}

	fsk := strField(raw, "fsk", "key", "token")
	if fsk == "" {
		fsk = "1774599378987"
	}

	return alphaBootstrap{
		FSK:     fsk,
		QuoteFS: "",
		LVS:     "",
	}, nil
}

func quoteFSFromLVS(lvs string) string {
	if len(lvs) != 6 {
		return lvs
	}
	return lvs[4:6] + "-" + lvs[2:4] + "-" + lvs[0:2]
}

func applyTokens(path string, boot alphaBootstrap) string {
	replacer := strings.NewReplacer(
		"{{fsk}}", boot.FSK,
		"{{quote_fs}}", boot.QuoteFS,
		"{{lvs}}", boot.LVS,
	)
	return replacer.Replace(path)
}

func extractDataPageProps(payload []byte) (map[string]any, error) {
	body := string(payload)
	raw := extractBetween(body, `data-page="`, `"`)
	if raw == "" {
		raw = extractBetween(body, `data-page</span>="<span class="html-attribute-value">`, `</span>"`)
	}
	if raw == "" {
		return nil, fmt.Errorf("data-page payload not found")
	}
	decoded := html.UnescapeString(html.UnescapeString(raw))

	var page struct {
		Props map[string]any `json:"props"`
	}
	if err := json.Unmarshal([]byte(decoded), &page); err != nil {
		return nil, err
	}
	if len(page.Props) == 0 {
		return nil, fmt.Errorf("data-page props missing")
	}
	return page.Props, nil
}

func extractBetween(value, start, end string) string {
	startIdx := strings.Index(value, start)
	if startIdx < 0 {
		return ""
	}
	startIdx += len(start)
	endIdx := strings.Index(value[startIdx:], end)
	if endIdx < 0 {
		return ""
	}
	return value[startIdx : startIdx+endIdx]
}

func nestedMap(row map[string]any, key string) map[string]any {
	value, ok := row[key]
	if !ok {
		return nil
	}
	out, _ := value.(map[string]any)
	return out
}

func latestQuarterMetrics(rows []map[string]any) map[string]any {
	type metric struct {
		date  time.Time
		value any
	}
	latest := map[string]metric{}
	for _, row := range rows {
		particular := strField(row, "particulars")
		if particular == "" {
			continue
		}
		date := timeField(row, "financial_date")
		current, exists := latest[particular]
		if !exists || date.After(current.date) {
			latest[particular] = metric{date: date, value: row["value"]}
		}
	}
	out := make(map[string]any, len(latest)+1)
	for key, value := range latest {
		out[key] = value.value
		if storedDate, ok := out["financial_date"].(time.Time); !ok || value.date.After(storedDate) {
			out["financial_date"] = value.date.Format("2006-01-02")
		}
	}
	return out
}

func scoreFundamentals(f domain.Fundamentals, sectorPE, sectorPB float64) float64 {
	scores := make([]float64, 0, 6)

	if f.EPS != 0 {
		if f.EPS > 0 {
			scores = append(scores, 1)
		} else {
			scores = append(scores, 0.1)
		}
	}
	if f.ROE != 0 {
		scores = append(scores, clamp01(0.5+(f.ROE/25)))
	}
	if f.PE != 0 {
		switch {
		case f.PE <= 0:
			scores = append(scores, 0.1)
		case sectorPE > 0:
			scores = append(scores, clamp01(1-((f.PE-sectorPE)/sectorPE)*0.5))
		default:
			scores = append(scores, clamp01(1-(f.PE/80)))
		}
	}
	if f.PB != 0 {
		switch {
		case f.PB <= 0:
			scores = append(scores, 0.1)
		case sectorPB > 0:
			scores = append(scores, clamp01(1-((f.PB-sectorPB)/sectorPB)*0.6))
		default:
			scores = append(scores, clamp01(1-(f.PB/12)))
		}
	}
	if f.BVPS > 0 {
		priceToBook := 0.0
		if f.PB > 0 {
			priceToBook = f.PB
		}
		if priceToBook == 0 {
			scores = append(scores, 0.5)
		} else {
			scores = append(scores, clamp01(1-(priceToBook/10)))
		}
	}
	if f.RevenueTTMYOY != 0 || f.NetProfitTTMYOY != 0 {
		growthScore := 0.5
		if f.RevenueTTMYOY > 0 {
			growthScore += 0.25
		}
		if f.NetProfitTTMYOY > 0 {
			growthScore += 0.25
		}
		scores = append(scores, clamp01(growthScore))
	}

	if len(scores) == 0 {
		return 0.5
	}
	sum := 0.0
	for _, score := range scores {
		sum += score
	}
	return sum / float64(len(scores))
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

func normalizeAlphaBaseURL(value string) string {
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, "https://www.nepsealpha.com") {
		return strings.Replace(trimmed, "https://www.nepsealpha.com", "https://nepsealpha.com", 1)
	}
	if strings.HasPrefix(trimmed, "http://www.nepsealpha.com") {
		return strings.Replace(trimmed, "http://www.nepsealpha.com", "https://nepsealpha.com", 1)
	}
	return trimmed
}

func normalizePercentLike(value float64) float64 {
	switch {
	case value == 0:
		return 0
	case math.Abs(value) <= 1:
		return value * 100
	default:
		return value
	}
}

func normalizeBVPS(raw, ltp, pb float64) float64 {
	if pb > 0 && ltp > 0 {
		derived := ltp / pb
		if raw <= 0 || math.Abs(raw-derived) > math.Max(derived*5, 1000) {
			return derived
		}
	}
	return raw
}

func minLen(values ...int) int {
	if len(values) == 0 {
		return 0
	}
	min := values[0]
	for _, value := range values[1:] {
		if value < min {
			min = value
		}
	}
	return min
}
