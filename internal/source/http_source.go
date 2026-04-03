package source

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"stonks/internal/domain"
)

type HTTPSourceConfig struct {
	Name                  string
	BaseURL               string
	QuotePath             string
	FloorsheetPath        string
	BearerToken           string
	Headers               map[string]string
	TimeoutSeconds        int
	UserAgent             string
	Referer               string
	RequestDelayMS        int
	InsecureSkipTLSVerify bool
}

type HTTPSource struct {
	name        string
	baseURL     string
	quotePath   string
	floorPath   string
	bearerToken string
	headers     map[string]string
	client      *http.Client
	userAgent   string
	referer     string
	delay       time.Duration
}

func NewHTTPSource(cfg HTTPSourceConfig) *HTTPSource {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	return &HTTPSource{
		name:        cfg.Name,
		baseURL:     strings.TrimRight(cfg.BaseURL, "/"),
		quotePath:   cfg.QuotePath,
		floorPath:   cfg.FloorsheetPath,
		bearerToken: cfg.BearerToken,
		headers:     cfg.Headers,
		userAgent:   cfg.UserAgent,
		referer:     cfg.Referer,
		delay:       time.Duration(cfg.RequestDelayMS) * time.Millisecond,
		client: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: cfg.InsecureSkipTLSVerify},
			},
		},
	}
}

func (s *HTTPSource) Name() string {
	return s.name
}

func (s *HTTPSource) Fetch(ctx context.Context) (domain.MarketSnapshot, error) {
	quotes, err := s.fetchQuotes(ctx)
	if err != nil {
		return domain.MarketSnapshot{}, fmt.Errorf("%s quote fetch: %w", s.name, err)
	}
	if err := s.pause(ctx); err != nil {
		return domain.MarketSnapshot{}, err
	}
	trades, err := s.fetchTrades(ctx)
	if err != nil {
		return domain.MarketSnapshot{}, fmt.Errorf("%s floorsheet fetch: %w", s.name, err)
	}
	return domain.MarketSnapshot{
		Quotes: quotes,
		Trades: trades,
	}, nil
}

func (s *HTTPSource) fetchQuotes(ctx context.Context) ([]domain.Quote, error) {
	payload, err := s.get(ctx, s.quotePath)
	if err != nil {
		return nil, err
	}
	rows, err := parseRows(payload)
	if err != nil {
		return nil, err
	}
	quotes := make([]domain.Quote, 0, len(rows))
	now := time.Now().UTC()
	for _, row := range rows {
		symbol := strField(row, "symbol", "stockSymbol", "securitySymbol", "ticker")
		if symbol == "" {
			continue
		}
		quotes = append(quotes, domain.Quote{
			Symbol:      symbol,
			LastPrice:   numField(row, "lastPrice", "ltp", "closePrice", "closingPrice"),
			OpenPrice:   numField(row, "openPrice", "open"),
			HighPrice:   numField(row, "highPrice", "high"),
			LowPrice:    numField(row, "lowPrice", "low"),
			PrevClose:   numField(row, "previousClosing", "previousClose", "prevClose", "previous_close"),
			Volume:      numField(row, "totalTradedQuantity", "volume", "qty"),
			Turnover:    numField(row, "totalTradedValue", "turnover", "ltv"),
			CollectedAt: now,
		})
	}
	return quotes, nil
}

func (s *HTTPSource) fetchTrades(ctx context.Context) ([]domain.Trade, error) {
	payload, err := s.get(ctx, s.floorPath)
	if err != nil {
		return nil, err
	}
	rows, err := parseRows(payload)
	if err != nil {
		return nil, err
	}
	trades := make([]domain.Trade, 0, len(rows))
	for _, row := range rows {
		symbol := strField(row, "symbol", "stockSymbol", "securitySymbol", "ticker")
		if symbol == "" {
			continue
		}
		trades = append(trades, domain.Trade{
			Symbol:       symbol,
			Price:        numField(row, "tradePrice", "contractRate", "price", "ltp", "rt"),
			Quantity:     numField(row, "tradeQuantity", "contractQuantity", "quantity", "qty", "qnt"),
			BuyerBroker:  strField(row, "buyerMemberId", "buyerBrokerName", "buyerBroker", "bb"),
			SellerBroker: strField(row, "sellerMemberId", "sellerBrokerName", "sellerBroker", "sb"),
			TradedAt:     timeField(row, "businessDate", "tradeTime", "tradedAt", "date"),
		})
	}
	return trades, nil
}

func (s *HTTPSource) get(ctx context.Context, path string) ([]byte, error) {
	return s.getWithAccept(ctx, path, "application/json, text/plain, */*")
}

func (s *HTTPSource) getWithAccept(ctx context.Context, path, accept string) ([]byte, error) {
	return s.getWithOptions(ctx, path, accept, "", nil)
}

func (s *HTTPSource) getWithOptions(ctx context.Context, path, accept, referer string, extraHeaders map[string]string) ([]byte, error) {
	if path == "" {
		return nil, fmt.Errorf("empty path")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(accept) != "" {
		req.Header.Set("Accept", accept)
	}
	if s.userAgent != "" {
		req.Header.Set("User-Agent", s.userAgent)
	}
	if referer != "" {
		req.Header.Set("Referer", referer)
	} else if s.referer != "" {
		req.Header.Set("Referer", s.referer)
	}
	if s.bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.bearerToken)
	}
	for key, value := range s.headers {
		req.Header.Set(key, value)
	}
	for key, value := range extraHeaders {
		req.Header.Set(key, value)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return io.ReadAll(resp.Body)
}

func (s *HTTPSource) pause(ctx context.Context) error {
	if s.delay <= 0 {
		return nil
	}
	timer := time.NewTimer(s.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func parseRows(payload []byte) ([]map[string]any, error) {
	var raw any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, err
	}
	rows := collectRows(raw)
	if len(rows) == 0 {
		return nil, fmt.Errorf("unsupported or empty response shape %T", raw)
	}
	return rows, nil
}

func collectMaps(items []any) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if row, ok := item.(map[string]any); ok {
			out = append(out, row)
		}
	}
	return out
}

func collectRows(raw any) []map[string]any {
	switch typed := raw.(type) {
	case []any:
		return collectMaps(typed)
	case map[string]any:
		for _, key := range []string{"content", "data", "result", "payload", "rows", "list", "live_data"} {
			if value, ok := typed[key]; ok {
				if rows := collectRows(value); len(rows) > 0 {
					return rows
				}
			}
		}
		return []map[string]any{typed}
	default:
		return nil
	}
}
