package domain

import "time"

type Quote struct {
	Symbol      string    `json:"symbol"`
	LastPrice   float64   `json:"last_price"`
	OpenPrice   float64   `json:"open_price"`
	HighPrice   float64   `json:"high_price"`
	LowPrice    float64   `json:"low_price"`
	PrevClose   float64   `json:"prev_close"`
	Volume      float64   `json:"volume"`
	Turnover    float64   `json:"turnover"`
	CollectedAt time.Time `json:"collected_at"`
}

type Trade struct {
	Symbol       string    `json:"symbol"`
	Price        float64   `json:"price"`
	Quantity     float64   `json:"quantity"`
	BuyerBroker  string    `json:"buyer_broker"`
	SellerBroker string    `json:"seller_broker"`
	TradedAt     time.Time `json:"traded_at"`
}

type MarketSnapshot struct {
	Quotes []Quote `json:"quotes"`
	Trades []Trade `json:"trades"`
}

type Candle struct {
	Symbol    string    `json:"symbol"`
	Open      float64   `json:"open"`
	High      float64   `json:"high"`
	Low       float64   `json:"low"`
	Close     float64   `json:"close"`
	Volume    float64   `json:"volume"`
	Timestamp time.Time `json:"timestamp"`
}

type Fundamentals struct {
	Symbol           string    `json:"symbol"`
	CompanyName      string    `json:"company_name"`
	Sector           string    `json:"sector"`
	PE               float64   `json:"pe"`
	PB               float64   `json:"pb"`
	PEG              float64   `json:"peg"`
	ROE              float64   `json:"roe"`
	ROA              float64   `json:"roa"`
	EPS              float64   `json:"eps"`
	EPSTTM           float64   `json:"eps_ttm"`
	BVPS             float64   `json:"bvps"`
	RevenueTTMYOY    float64   `json:"revenue_ttm_yoy"`
	NetProfitTTMYOY  float64   `json:"net_profit_ttm_yoy"`
	DividendYield    float64   `json:"dividend_yield"`
	PromoterHolding  float64   `json:"promoter_holding"`
	PublicHolding    float64   `json:"public_holding"`
	FundamentalScore float64   `json:"fundamental_score"`
	FinancialDate    time.Time `json:"financial_date"`
	Loaded           bool      `json:"loaded"`
}

type Recommendation struct {
	Symbol             string    `json:"symbol"`
	Source             string    `json:"source"`
	Mode               string    `json:"mode"`
	Verdict            string    `json:"verdict"`
	BuyPrice           float64   `json:"buy_price"`
	StopLoss           float64   `json:"stop_loss"`
	TakeProfit         float64   `json:"take_profit"`
	RiskReward         float64   `json:"risk_reward"`
	RiskRewardRatio    string    `json:"risk_reward_ratio"`
	MomentumScore      float64   `json:"momentum_score"`
	LiquidityScore     float64   `json:"liquidity_score"`
	FloorsheetScore    float64   `json:"floorsheet_score"`
	IndicatorScore     float64   `json:"indicator_score"`
	FundamentalScore   float64   `json:"fundamental_score"`
	RSI14              float64   `json:"rsi_14,omitempty"`
	EMA20              float64   `json:"ema_20,omitempty"`
	EMA50              float64   `json:"ema_50,omitempty"`
	ATR14              float64   `json:"atr_14,omitempty"`
	PE                 float64   `json:"pe,omitempty"`
	PB                 float64   `json:"pb,omitempty"`
	ROE                float64   `json:"roe,omitempty"`
	EPS                float64   `json:"eps,omitempty"`
	BVPS               float64   `json:"bvps,omitempty"`
	IndicatorsLoaded   bool      `json:"indicators_loaded"`
	FundamentalsLoaded bool      `json:"fundamentals_loaded"`
	Confidence         float64   `json:"confidence"`
	Reasoning          []string  `json:"reasoning"`
	GeneratedAt        time.Time `json:"generated_at"`
}

type AnalysisResult struct {
	MarketStatus      string             `json:"market_status"`
	Mode              string             `json:"mode"`
	RecommendationSet []Recommendation   `json:"recommendations"`
	DiscardedSymbols  []Discarded        `json:"discarded_symbols,omitempty"`
	LatestQuotes      map[string]float64 `json:"-"`
	AnalyzedAt        time.Time          `json:"-"`
}

type Discarded struct {
	Symbol string `json:"symbol"`
	Reason string `json:"reason"`
}
