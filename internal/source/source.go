package source

import (
	"context"

	"stonks/internal/domain"
)

type Source interface {
	Name() string
	Fetch(context.Context) (domain.MarketSnapshot, error)
}

type CandleProvider interface {
	FetchCandles(ctx context.Context, symbol, resolution string, frame int) ([]domain.Candle, error)
}

type FundamentalProvider interface {
	FetchFundamentals(ctx context.Context, symbol string) (domain.Fundamentals, error)
}
