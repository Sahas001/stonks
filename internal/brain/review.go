package brain

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"stonks/internal/config"
	"stonks/internal/domain"
	"stonks/internal/trades"
)

type Review struct {
	Verdict string
	Stance  string
	Risk    string
	Note    string
	Lesson  string
}

type Client struct {
	cfg config.Config
}

func New(cfg config.Config) *Client {
	return &Client{cfg: cfg}
}

func (c *Client) Enabled() bool {
	return c.cfg.AIReviewEnabled && strings.TrimSpace(c.cfg.AIReviewCommand) != ""
}

func (c *Client) ReviewRecommendation(ctx context.Context, result domain.AnalysisResult) (*Review, error) {
	if !c.Enabled() || len(result.RecommendationSet) == 0 {
		return nil, nil
	}

	best := result.RecommendationSet[0]
	prompt := strings.Join([]string{
		reviewerInstructions(),
		"",
		"Task: Review this NEPSE stock recommendation. Decide whether it is a buy, only a watchlist name, or should be avoided.",
		"",
		"Memory:",
		c.readMemory(),
		"",
		"Snapshot:",
		formatRecommendation(result, best),
	}, "\n")

	review, err := c.run(ctx, prompt)
	if err != nil || review == nil {
		return review, err
	}
	review = normalizeRecommendationReview(review, best)

	snippet := fmt.Sprintf("%s [pick] %s mode=%s verdict=%s rr=%s conf=%.2f stance=%s note=%s",
		time.Now().Format(time.RFC3339),
		best.Symbol,
		strings.ToUpper(result.Mode),
		review.Verdict,
		best.RiskRewardRatio,
		best.Confidence,
		review.Stance,
		shorten(review.Note, 90),
	)
	_ = c.appendMemory(snippet)
	return review, nil
}

func (c *Client) RememberFact(snippet string) error {
	if strings.TrimSpace(snippet) == "" {
		return nil
	}
	return c.appendMemory(fmt.Sprintf("%s [fact] %s", time.Now().Format(time.RFC3339), snippet))
}

func (c *Client) ReviewTrade(ctx context.Context, side string, entry trades.Entry, position *trades.Position, summary trades.Summary) (*Review, error) {
	if !c.Enabled() {
		return nil, nil
	}

	prompt := strings.Join([]string{
		reviewerInstructions(),
		"",
		"Task: Review this executed trade. Give short feedback on discipline, risk, and what matters next.",
		"",
		"Memory:",
		c.readMemory(),
		"",
		"Trade:",
		formatTrade(side, entry, position, summary),
	}, "\n")

	review, err := c.run(ctx, prompt)
	if err != nil || review == nil {
		return review, err
	}
	review = normalizeTradeReview(review, side)

	snippet := fmt.Sprintf("%s [trade] %s %s qty=%.2f px=%.2f verdict=%s stance=%s note=%s",
		time.Now().Format(time.RFC3339),
		strings.ToUpper(side),
		entry.Symbol,
		entry.Quantity,
		entry.Price,
		review.Verdict,
		review.Stance,
		shorten(review.Note, 90),
	)
	if side == "sell" {
		if closed := latestClosedTrade(summary, entry.Symbol); closed != nil {
			snippet += fmt.Sprintf(" pnl=%.2f(%.2f%%)", closed.RealizedPnL, closed.RealizedPct)
		}
	}
	_ = c.appendMemory(snippet)
	return review, nil
}

func (c *Client) run(ctx context.Context, prompt string) (*Review, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.cfg.AIReviewTimeoutSeconds)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "zsh", "-lc", c.cfg.AIReviewCommand)
	cmd.Stdin = strings.NewReader(prompt)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("local ai review failed: %w", err)
	}
	return parseReview(stdout.String()), nil
}

func reviewerInstructions() string {
	return strings.Join([]string{
		"You are the local Stonks review layer.",
		"Use only the supplied data.",
		"Do not invent prices, indicators, fundamentals, or news.",
		"Be concise and avoid hype.",
		"If unsure, prefer WATCHLIST and CAUTIOUS over BUY and BULLISH.",
		"Return exactly five lines:",
		"VERDICT: buy|watchlist|avoid",
		"STANCE: bullish|cautious|bearish",
		"RISK: one short sentence",
		"NOTE: one short sentence",
		"LESSON: one short sentence",
	}, "\n")
}

func formatRecommendation(result domain.AnalysisResult, best domain.Recommendation) string {
	lines := []string{
		"status=" + result.MarketStatus,
		"mode=" + strings.ToUpper(result.Mode),
		"engine_verdict=" + best.Verdict,
		fmt.Sprintf("symbol=%s source=%s", best.Symbol, best.Source),
		fmt.Sprintf("buy=%.2f stop=%.2f target=%.2f rr=%s confidence=%.2f", best.BuyPrice, best.StopLoss, best.TakeProfit, best.RiskRewardRatio, best.Confidence),
		fmt.Sprintf("scores momentum=%.2f liquidity=%.2f flow=%.2f technical=%.2f fundamental=%.2f", best.MomentumScore, best.LiquidityScore, best.FloorsheetScore, best.IndicatorScore, best.FundamentalScore),
	}
	if best.IndicatorsLoaded {
		lines = append(lines, fmt.Sprintf("indicators rsi14=%.2f ema20=%.2f ema50=%.2f atr14=%.2f", best.RSI14, best.EMA20, best.EMA50, best.ATR14))
	}
	if best.FundamentalsLoaded {
		lines = append(lines, fmt.Sprintf("fundamentals pe=%.2f pb=%.2f roe=%.2f eps=%.2f bvps=%.2f", best.PE, best.PB, best.ROE, best.EPS, best.BVPS))
	}
	if len(best.Reasoning) > 0 {
		lines = append(lines, "reasoning="+strings.Join(best.Reasoning, " | "))
	}
	return strings.Join(lines, "\n")
}

func formatTrade(side string, entry trades.Entry, position *trades.Position, summary trades.Summary) string {
	lines := []string{
		fmt.Sprintf("side=%s symbol=%s qty=%.2f price=%.2f time=%s", strings.ToUpper(side), entry.Symbol, entry.Quantity, entry.Price, entry.RecordedAt.Local().Format(time.RFC3339)),
		fmt.Sprintf("portfolio realized_pnl=%.2f unrealized_pnl=%.2f open_positions=%d", summary.RealizedPnL, summary.UnrealizedPnL, summary.OpenPositions),
	}
	if position != nil {
		lines = append(lines, fmt.Sprintf("position qty=%.2f avg_cost=%.2f last=%.2f unrealized_pnl=%.2f unrealized_pct=%.2f", position.Quantity, position.AverageCost, position.MarketPrice, position.UnrealizedPnL, position.UnrealizedPct))
	} else {
		lines = append(lines, "position flat")
	}
	if closed := latestClosedTrade(summary, entry.Symbol); closed != nil {
		lines = append(lines, fmt.Sprintf("latest_closed_trade avg_buy=%.2f sell=%.2f realized_pnl=%.2f realized_pct=%.2f", closed.AverageBuy, closed.SellPrice, closed.RealizedPnL, closed.RealizedPct))
	}
	if note := strings.TrimSpace(entry.Note); note != "" {
		lines = append(lines, "user_note="+note)
	}
	return strings.Join(lines, "\n")
}

func latestClosedTrade(summary trades.Summary, symbol string) *trades.ClosedTrade {
	for i := len(summary.ClosedTrades) - 1; i >= 0; i-- {
		if strings.EqualFold(summary.ClosedTrades[i].Symbol, symbol) {
			return &summary.ClosedTrades[i]
		}
	}
	return nil
}

func parseReview(raw string) *Review {
	clean := cleanOutput(raw)
	if clean == "" {
		return nil
	}

	review := &Review{}
	for _, line := range strings.Split(clean, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(strings.ToUpper(line), "VERDICT:"):
			review.Verdict = strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
		case strings.HasPrefix(strings.ToUpper(line), "STANCE:"):
			review.Stance = strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
		case strings.HasPrefix(strings.ToUpper(line), "RISK:"):
			review.Risk = strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
		case strings.HasPrefix(strings.ToUpper(line), "NOTE:"):
			review.Note = strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
		case strings.HasPrefix(strings.ToUpper(line), "LESSON:"):
			review.Lesson = strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
		}
	}
	if review.Verdict == "" && review.Stance == "" && review.Risk == "" && review.Note == "" && review.Lesson == "" {
		lines := strings.Split(clean, "\n")
		review.Verdict = "watchlist"
		review.Stance = "cautious"
		review.Note = shorten(strings.Join(lines, " "), 180)
	}
	return review
}

func normalizeRecommendationReview(review *Review, best domain.Recommendation) *Review {
	if review == nil {
		review = &Review{}
	}
	if invalidVerdict(review.Verdict) {
		review.Verdict = ""
	}
	if invalidStance(review.Stance) {
		review.Stance = ""
	}
	if invalidFreeform(review.Risk) {
		review.Risk = ""
	}
	if invalidFreeform(review.Note) {
		review.Note = ""
	}
	if invalidFreeform(review.Lesson) {
		review.Lesson = ""
	}
	if strings.TrimSpace(review.Verdict) == "" {
		review.Verdict = best.Verdict
	}
	if strings.TrimSpace(review.Stance) == "" {
		switch strings.ToLower(strings.TrimSpace(review.Verdict)) {
		case "buy":
			review.Stance = "bullish"
		case "avoid":
			review.Stance = "bearish"
		default:
			review.Stance = "cautious"
		}
	}
	if strings.EqualFold(strings.TrimSpace(review.Verdict), "watchlist") && strings.EqualFold(strings.TrimSpace(review.Stance), "bullish") {
		review.Stance = "cautious"
	}
	if strings.TrimSpace(review.Risk) == "" {
		switch strings.ToLower(strings.TrimSpace(review.Verdict)) {
		case "buy":
			review.Risk = "Treat this as an actionable setup only if volume and conviction stay intact."
		case "avoid":
			review.Risk = "The setup quality is not strong enough to justify a fresh entry."
		default:
			review.Risk = "The setup has some merit but does not yet justify taking an entry."
		}
	}
	if strings.TrimSpace(review.Note) == "" {
		switch strings.ToLower(strings.TrimSpace(review.Verdict)) {
		case "buy":
			review.Note = "The engine sees enough alignment to justify a trade plan."
		case "avoid":
			review.Note = "Capital is better kept aside until the setup improves."
		default:
			review.Note = "Keep it on watch and wait for stronger confirmation."
		}
	}
	return review
}

func normalizeTradeReview(review *Review, side string) *Review {
	if review == nil {
		review = &Review{}
	}
	if invalidVerdict(review.Verdict) {
		review.Verdict = ""
	}
	if invalidStance(review.Stance) {
		review.Stance = ""
	}
	if invalidFreeform(review.Risk) {
		review.Risk = ""
	}
	if invalidFreeform(review.Note) {
		review.Note = ""
	}
	if invalidFreeform(review.Lesson) {
		review.Lesson = ""
	}
	if strings.TrimSpace(review.Verdict) == "" {
		if strings.EqualFold(side, "buy") {
			review.Verdict = "watchlist"
		} else {
			review.Verdict = "avoid"
		}
	}
	if strings.TrimSpace(review.Stance) == "" {
		review.Stance = "cautious"
	}
	if strings.TrimSpace(review.Risk) == "" {
		review.Risk = "Review execution discipline and position sizing before repeating this trade pattern."
	}
	if strings.TrimSpace(review.Note) == "" {
		if strings.EqualFold(side, "sell") {
			review.Note = "Use the realized outcome to refine exits and future conviction."
		} else {
			review.Note = "Make sure the entry thesis is clearer than simple momentum chasing."
		}
	}
	return review
}

func cleanOutput(raw string) string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	for {
		start := strings.Index(raw, "<think>")
		end := strings.Index(raw, "</think>")
		if start == -1 || end == -1 || end < start {
			break
		}
		raw = raw[:start] + raw[end+len("</think>"):]
	}
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func (c *Client) readMemory() string {
	data, err := os.ReadFile(c.cfg.ModelFilePath)
	if err != nil || len(data) == 0 {
		return "No prior compact memory recorded yet."
	}
	return strings.TrimSpace(string(data))
}

func (c *Client) appendMemory(snippet string) error {
	if strings.TrimSpace(snippet) == "" {
		return nil
	}

	lines := make([]string, 0, c.cfg.ModelFileMaxEntries)
	if existing, err := os.ReadFile(c.cfg.ModelFilePath); err == nil {
		for _, line := range strings.Split(string(existing), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "- ") {
				lines = append(lines, line)
			}
		}
	}
	lines = append(lines, "- "+snippet)
	if max := c.cfg.ModelFileMaxEntries; max > 0 && len(lines) > max {
		lines = lines[len(lines)-max:]
	}

	header := []string{
		"# STONKS LOCAL BRAIN",
		"# Keep these notes short. They are rolling lessons, not ground truth.",
		"# Use them to stay consistent and cautious.",
		"",
	}
	content := strings.Join(append(header, lines...), "\n") + "\n"
	maxBytes := c.cfg.ModelFileMaxBytes
	for maxBytes > 0 && len(content) > maxBytes && len(lines) > 1 {
		lines = lines[1:]
		content = strings.Join(append(header, lines...), "\n") + "\n"
	}

	if err := os.MkdirAll(filepath.Dir(c.cfg.ModelFilePath), 0o700); err != nil {
		return err
	}
	return os.WriteFile(c.cfg.ModelFilePath, []byte(content), 0o600)
}

func shorten(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}

func invalidVerdict(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return false
	}
	switch value {
	case "buy", "watchlist", "avoid":
		return false
	}
	return strings.Contains(value, "|")
}

func invalidStance(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return false
	}
	switch value {
	case "bullish", "cautious", "bearish":
		return false
	}
	return strings.Contains(value, "|")
}

func invalidFreeform(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return false
	}
	switch value {
	case "one short sentence", "one sentence", "short sentence":
		return true
	}
	return strings.Contains(value, "one short sentence")
}
