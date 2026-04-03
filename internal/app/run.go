package app

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"stonks/internal/analyzer"
	"stonks/internal/brain"
	"stonks/internal/config"
	"stonks/internal/domain"
	"stonks/internal/journal"
	"stonks/internal/source"
	"stonks/internal/trades"
)

const (
	colorReset   = "\033[0m"
	colorBold    = "\033[1m"
	colorRed     = "\033[31m"
	colorGreen   = "\033[32m"
	colorYellow  = "\033[33m"
	colorBlue    = "\033[34m"
	colorMagenta = "\033[35m"
	colorCyan    = "\033[36m"
	colorWhite   = "\033[37m"
	colorGray    = "\033[90m"
)

func Run(ctx context.Context, args []string) error {
	cfg := config.Load()

	sources, err := buildSources(cfg)
	if err != nil {
		return err
	}

	engine := analyzer.New(cfg, sources)
	reviewer := brain.New(cfg)
	return runCLI(ctx, cfg, engine, reviewer, sources, args)
}

func buildSources(cfg config.Config) ([]source.Source, error) {
	var sources []source.Source
	for _, name := range cfg.EnabledSources {
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "nepse":
			sources = append(sources, source.NewHTTPSource(source.HTTPSourceConfig{
				Name:                  "nepse",
				BaseURL:               cfg.NepseBaseURL,
				QuotePath:             cfg.NepseQuotePath,
				FloorsheetPath:        cfg.NepseFloorsheetPath,
				BearerToken:           cfg.NepseAuthToken,
				Headers:               cfg.NepseHeaders,
				TimeoutSeconds:        cfg.HTTPTimeoutSeconds,
				UserAgent:             cfg.HTTPUserAgent,
				Referer:               cfg.HTTPReferer,
				RequestDelayMS:        cfg.RequestDelayMS,
				InsecureSkipTLSVerify: cfg.InsecureSkipTLSVerify,
			}))
		case "alpha", "nepsealpha":
			if cfg.AlphaQuotePath == "" || cfg.AlphaFloorsheetPath == "" {
				return nil, fmt.Errorf("alpha source enabled but STONKS_ALPHA_QUOTE_PATH or STONKS_ALPHA_FLOORSHEET_PATH is empty")
			}
			sources = append(sources, source.NewAlphaSource(source.AlphaSourceConfig{
				Base: source.HTTPSourceConfig{
					Name:                  "nepsealpha",
					BaseURL:               cfg.AlphaBaseURL,
					QuotePath:             cfg.AlphaQuotePath,
					FloorsheetPath:        cfg.AlphaFloorsheetPath,
					BearerToken:           cfg.AlphaToken,
					Headers:               withCookie(cfg.AlphaHeaders, cfg.AlphaCookieName, cfg.AlphaCookieValue),
					TimeoutSeconds:        cfg.HTTPTimeoutSeconds,
					UserAgent:             cfg.HTTPUserAgent,
					Referer:               cfg.HTTPReferer,
					RequestDelayMS:        cfg.RequestDelayMS,
					InsecureSkipTLSVerify: cfg.InsecureSkipTLSVerify,
				},
				ForceURLKeyPath: cfg.AlphaForceURLKeyPath,
				ForceURLKeyFS:   cfg.AlphaForceURLKeyFS,
				HistoryPath:     cfg.AlphaHistoryPath,
				ProfilePath:     cfg.AlphaProfilePath,
			}))
		default:
			return nil, fmt.Errorf("unsupported source: %s", name)
		}
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("no sources configured")
	}
	return sources, nil
}

func withCookie(headers map[string]string, cookieName, cookieValue string) map[string]string {
	if cookieName == "" || cookieValue == "" {
		return headers
	}

	out := make(map[string]string, len(headers)+1)
	for key, value := range headers {
		out[key] = value
	}

	if existing, ok := out["Cookie"]; ok && strings.TrimSpace(existing) != "" {
		out["Cookie"] = existing + "; " + cookieName + "=" + cookieValue
		return out
	}

	out["Cookie"] = cookieName + "=" + cookieValue
	return out
}

func runCLI(ctx context.Context, cfg config.Config, engine *analyzer.Engine, reviewer *brain.Client, sources []source.Source, args []string) error {
	command := "picks"
	commandArgs := []string{}
	if len(args) > 0 {
		command = strings.ToLower(strings.TrimSpace(args[0]))
		commandArgs = args[1:]
	}
	if looksLikeTicker(command) {
		commandArgs = append([]string{"--tick=" + strings.ToUpper(strings.TrimSpace(command))}, commandArgs...)
		command = "picks"
	}
	if strings.HasPrefix(command, "tick=") {
		commandArgs = append([]string{command}, commandArgs...)
		command = "picks"
	}

	switch command {
	case "", "picks", "analyze":
		mode := parseModeArg(commandArgs)
		symbol := parseTickArg(commandArgs)
		result, err := engine.AnalyzeSymbol(ctx, mode, symbol)
		if err != nil {
			return err
		}
		return writeResponse(ctx, os.Stdout, cfg, reviewer, result, 0, false, journalEntryType(symbol))
	case "top":
		limit := 3
		mode := parseModeArg(commandArgs)
		symbol := parseTickArg(commandArgs)
		if len(commandArgs) > 0 && !strings.HasPrefix(commandArgs[0], "--") {
			if parsed, err := strconv.Atoi(commandArgs[0]); err == nil && parsed > 0 {
				limit = parsed
			}
		}
		result, err := engine.AnalyzeSymbol(ctx, mode, symbol)
		if err != nil {
			return err
		}
		return writeResponse(ctx, os.Stdout, cfg, reviewer, result, limit, false, journalEntryType(symbol))
	case "debug":
		mode := parseModeArg(commandArgs)
		symbol := parseTickArg(commandArgs)
		result, err := engine.AnalyzeSymbol(ctx, mode, symbol)
		if err != nil {
			return err
		}
		return writeResponse(ctx, os.Stdout, cfg, reviewer, result, 0, true, journalEntryType(symbol))
	case "help", "-h", "--help":
		printHelp()
		return nil
	case "set-cookie":
		if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
			return fmt.Errorf("usage: stonks set-cookie <cookie_value>")
		}
		return setCookieEnv(cfg.EnvPath, "nepsealpha_session", args[1])
	case "doctor":
		return runDoctor(os.Stdout, cfg, reviewer)
	case "performance", "accuracy":
		return runPerformance(os.Stdout, cfg)
	case "buy", "sell":
		return runTradeCommand(ctx, os.Stdout, cfg, reviewer, sources, command, commandArgs)
	case "portfolio", "trades", "pnl":
		return runTradeReport(ctx, os.Stdout, cfg, sources, command)
	case "clear", "reset-data":
		return runResetData(os.Stdout, cfg, commandArgs)
	case "version":
		fmt.Fprintln(os.Stdout, "stonks v0.1.0")
		return nil
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

type analyzeResponse struct {
	MarketStatus        string                  `json:"market_status"`
	Mode                string                  `json:"mode"`
	BestPick            *domain.Recommendation  `json:"best_pick,omitempty"`
	Recommendations     []domain.Recommendation `json:"recommendations"`
	RecommendationCount int                     `json:"recommendation_count"`
	Summary             string                  `json:"summary"`
	Review              *journal.Review         `json:"review,omitempty"`
	AIReview            *brain.Review           `json:"ai_review,omitempty"`
	DiscardedSymbols    []domain.Discarded      `json:"discarded_symbols,omitempty"`
}

func newAnalyzeResponse(result domain.AnalysisResult, limit int, verbose bool) analyzeResponse {
	recommendations := result.RecommendationSet
	if limit > 0 && limit < len(recommendations) {
		recommendations = recommendations[:limit]
	}

	var best *domain.Recommendation
	if len(recommendations) > 0 {
		best = &recommendations[0]
	}

	response := analyzeResponse{
		MarketStatus:        result.MarketStatus,
		Mode:                result.Mode,
		BestPick:            best,
		Recommendations:     recommendations,
		RecommendationCount: len(recommendations),
		Summary:             summarizeRecommendations(result.MarketStatus, recommendations),
	}
	if verbose {
		response.DiscardedSymbols = result.DiscardedSymbols
	}
	return response
}

func writeResponse(ctx context.Context, file *os.File, cfg config.Config, reviewer *brain.Client, result domain.AnalysisResult, limit int, verbose bool, entryType string) error {
	response := newAnalyzeResponse(result, limit, verbose)
	review, err := journal.Record(
		cfg.JournalPath,
		cfg.JournalRetentionDays,
		cfg.JournalMaxEntries,
		cfg.PickAppendMinutes,
		entryType,
		result,
	)
	if err == nil {
		response.Review = review
		_ = journal.UpdateSummary(cfg.PerformanceSummaryPath, review)
		if reviewer != nil && review != nil {
			_ = reviewer.RememberFact(fmt.Sprintf(
				"journal reviewed=%d above_buy=%d take_profit=%d stop_loss=%d summary=%s",
				review.EvaluatedCount,
				review.AboveBuyCount,
				review.HitTakeProfitCount,
				review.HitStopLossCount,
				shorten(review.Summary, 140),
			))
		}
	}
	if reviewer != nil {
		if aiReview, err := reviewer.ReviewRecommendation(ctx, result); err == nil {
			response.AIReview = aiReview
		}
	}
	return renderAnalyzeResponse(file, response)
}

func journalEntryType(symbol string) string {
	if strings.TrimSpace(symbol) != "" {
		return "user_inspect"
	}
	return "bot_pick"
}

func summarizeRecommendations(status string, recommendations []domain.Recommendation) string {
	if len(recommendations) == 0 {
		if status == "no_candidates" {
			return "No stock currently passed the bot's risk/reward and floorsheet filters."
		}
		return "No recommendation available."
	}

	best := recommendations[0]
	return "Best current setup is " + best.Symbol +
		bestSummaryTail(best)
}

func bestSummaryTail(best domain.Recommendation) string {
	switch best.Verdict {
	case "buy":
		return " at buy " + strconv.FormatFloat(best.BuyPrice, 'f', -1, 64) +
			", stop loss " + strconv.FormatFloat(best.StopLoss, 'f', -1, 64) +
			", take profit " + strconv.FormatFloat(best.TakeProfit, 'f', -1, 64) +
			", risk/reward " + best.RiskRewardRatio + "."
	case "watchlist":
		return " as a watchlist candidate. No entry is recommended yet."
	default:
		return " but it should be avoided for now."
	}
}

func parseModeArg(args []string) analyzer.AnalysisMode {
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		switch {
		case strings.HasPrefix(arg, "--mode="):
			return analyzer.ParseMode(strings.TrimPrefix(arg, "--mode="))
		case arg == "--mode" && i+1 < len(args):
			return analyzer.ParseMode(args[i+1])
		}
	}
	return analyzer.ModeHybrid
}

func parseTickArg(args []string) string {
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		switch {
		case strings.HasPrefix(strings.ToLower(arg), "tick="):
			return strings.ToUpper(strings.TrimSpace(strings.SplitN(arg, "=", 2)[1]))
		case strings.HasPrefix(arg, "--tick="):
			return strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(arg, "--tick=")))
		case arg == "--tick" && i+1 < len(args):
			return strings.ToUpper(strings.TrimSpace(args[i+1]))
		}
	}
	return ""
}

func looksLikeTicker(value string) bool {
	if value == "" {
		return false
	}
	switch value {
	case "picks", "analyze", "top", "debug", "help", "-h", "--help", "set-cookie", "doctor", "performance", "accuracy", "buy", "sell", "portfolio", "trades", "pnl", "clear", "reset-data", "version":
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

func printHelp() {
	fmt.Fprintln(os.Stdout, style("stonks v0.1.0", colorBold, colorCyan))
	fmt.Fprintln(os.Stdout, "")
	fmt.Fprintln(os.Stdout, "Usage:")
	fmt.Fprintln(os.Stdout, "  stonks <command> [args]")
	fmt.Fprintln(os.Stdout, "")
	fmt.Fprintln(os.Stdout, "Commands:")
	fmt.Fprintln(os.Stdout, "  picks")
	fmt.Fprintln(os.Stdout, "    Show the bot's best available picks. Supports --mode and tick=SYMBOL.")
	fmt.Fprintln(os.Stdout, "  top [n]")
	fmt.Fprintln(os.Stdout, "    Show the top n picks. Default is 3. Supports --mode.")
	fmt.Fprintln(os.Stdout, "  debug")
	fmt.Fprintln(os.Stdout, "    Show picks with rejected-symbol diagnostics. Supports --mode.")
	fmt.Fprintln(os.Stdout, "  set-cookie <value>")
	fmt.Fprintln(os.Stdout, "    Save nepsealpha_session into the config env file.")
	fmt.Fprintln(os.Stdout, "  doctor")
	fmt.Fprintln(os.Stdout, "    Show config, cookie, journal, model brain, and analysis settings.")
	fmt.Fprintln(os.Stdout, "  performance")
	fmt.Fprintln(os.Stdout, "    Show rolling journal stats plus compact all-time performance summary.")
	fmt.Fprintln(os.Stdout, "  buy SYMBOL QTY PRICE")
	fmt.Fprintln(os.Stdout, "    Record a buy into the separate trade ledger.")
	fmt.Fprintln(os.Stdout, "  sell SYMBOL QTY PRICE")
	fmt.Fprintln(os.Stdout, "    Record a sell into the separate trade ledger.")
	fmt.Fprintln(os.Stdout, "  portfolio")
	fmt.Fprintln(os.Stdout, "    Show open positions with unrealized P&L.")
	fmt.Fprintln(os.Stdout, "  trades")
	fmt.Fprintln(os.Stdout, "    Show recent trade ledger entries.")
	fmt.Fprintln(os.Stdout, "  pnl")
	fmt.Fprintln(os.Stdout, "    Show realized and unrealized trading performance.")
	fmt.Fprintln(os.Stdout, "  clear [journal|trades|brain|performance|all]")
	fmt.Fprintln(os.Stdout, "    Remove local testing logs, performance summary, and rolling AI memory without touching config or cookies.")
	fmt.Fprintln(os.Stdout, "  version")
	fmt.Fprintln(os.Stdout, "    Show binary version.")
	fmt.Fprintln(os.Stdout, "  help")
	fmt.Fprintln(os.Stdout, "    Show this help.")
	fmt.Fprintln(os.Stdout, "")
	fmt.Fprintln(os.Stdout, "Examples:")
	fmt.Fprintln(os.Stdout, "  stonks doctor")
	fmt.Fprintln(os.Stdout, "  stonks performance")
	fmt.Fprintln(os.Stdout, "  stonks buy AKJCL 100 415")
	fmt.Fprintln(os.Stdout, "  stonks sell AKJCL 100 452")
	fmt.Fprintln(os.Stdout, "  stonks portfolio")
	fmt.Fprintln(os.Stdout, "  stonks clear")
	fmt.Fprintln(os.Stdout, "  stonks clear trades")
	fmt.Fprintln(os.Stdout, "  stonks clear performance")
	fmt.Fprintln(os.Stdout, "  stonks picks")
	fmt.Fprintln(os.Stdout, "  stonks picks --mode=fundamental")
	fmt.Fprintln(os.Stdout, "  stonks top 5 --mode=technical")
	fmt.Fprintln(os.Stdout, "  stonks --tick=SYMBOL")
	fmt.Fprintln(os.Stdout, "  stonks tick=SYMBOL")
	fmt.Fprintln(os.Stdout, "  stonks SYMBOL")
	fmt.Fprintln(os.Stdout, "  stonks tick=SYMBOL --mode=technical")
	fmt.Fprintln(os.Stdout, "  stonks top 5")
	fmt.Fprintln(os.Stdout, "  stonks set-cookie 'new_cookie_value'")
}

func setCookieEnv(envPath, cookieName, cookieValue string) error {
	if err := os.MkdirAll(filepath.Dir(envPath), 0o700); err != nil {
		return err
	}
	lines := make([]string, 0)
	foundName := false
	foundValue := false

	if file, err := os.Open(envPath); err == nil {
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := scanner.Text()
			trimmed := strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(trimmed, "STONKS_ALPHA_COOKIE_NAME="):
				lines = append(lines, "STONKS_ALPHA_COOKIE_NAME="+cookieName)
				foundName = true
			case strings.HasPrefix(trimmed, "STONKS_ALPHA_COOKIE_VALUE="):
				lines = append(lines, "STONKS_ALPHA_COOKIE_VALUE="+cookieValue)
				foundValue = true
			default:
				lines = append(lines, line)
			}
		}
		_ = file.Close()
	}

	if !foundName {
		lines = append(lines, "STONKS_ALPHA_COOKIE_NAME="+cookieName)
	}
	if !foundValue {
		lines = append(lines, "STONKS_ALPHA_COOKIE_VALUE="+cookieValue)
	}

	content := strings.Join(lines, "\n")
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}

	absPath, err := filepath.Abs(envPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(absPath, []byte(content), 0o600); err != nil {
		return err
	}

	fmt.Fprintln(os.Stdout, "updated", absPath)
	return nil
}

func runDoctor(file *os.File, cfg config.Config, reviewer *brain.Client) error {
	printBanner(file, "STONKS DOCTOR", colorCyan)
	fmt.Fprintln(file, "")
	printSection(file, "SYSTEM", colorCyan)
	w := new(tabwriter.Writer)
	w.Init(file, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "Version\t%s\n", "v0.1.0")
	fmt.Fprintf(w, "Config Dir\t%s\n", cfg.ConfigDir)
	fmt.Fprintf(w, "Env Path\t%s\n", cfg.EnvPath)
	fmt.Fprintf(w, "Sources\t%s\n", strings.Join(cfg.EnabledSources, ", "))
	fmt.Fprintf(w, "Alpha Base URL\t%s\n", cfg.AlphaBaseURL)
	fmt.Fprintf(w, "Cookie Name\t%s\n", cfg.AlphaCookieName)
	fmt.Fprintf(w, "Cookie Loaded\t%t\n", strings.TrimSpace(cfg.AlphaCookieValue) != "")
	fmt.Fprintf(w, "Cookie Length\t%d\n", len(cfg.AlphaCookieValue))
	fmt.Fprintf(w, "History\t%s x %d candles\n", cfg.HistoryResolution, cfg.HistoryFrame)
	fmt.Fprintf(w, "Inactivity Filter\t%d days / %d active sessions\n", cfg.InactiveLookbackDays, cfg.InactiveMinTradeDays)
	fmt.Fprintf(w, "Candle Shortlist\t%d\n", cfg.CandleShortlistSize)
	fmt.Fprintf(w, "Journal Path\t%s\n", cfg.JournalPath)
	fmt.Fprintf(w, "Trade Log Path\t%s\n", cfg.TradeLogPath)
	fmt.Fprintf(w, "ModelFile Path\t%s\n", cfg.ModelFilePath)
	fmt.Fprintf(w, "Performance Summary\t%s\n", cfg.PerformanceSummaryPath)
	fmt.Fprintf(w, "Journal Retention\t%d days / %d entries\n", cfg.JournalRetentionDays, cfg.JournalMaxEntries)
	fmt.Fprintf(w, "Pick Append Window\t%d minutes\n", cfg.PickAppendMinutes)
	fmt.Fprintf(w, "AI Review Enabled\t%t\n", cfg.AIReviewEnabled)
	fmt.Fprintf(w, "AI Review Ready\t%t\n", reviewer != nil && reviewer.Enabled())
	fmt.Fprintf(w, "AI Review Command\t%s\n", cfg.AIReviewCommand)
	fmt.Fprintf(w, "AI Review Timeout\t%d s\n", cfg.AIReviewTimeoutSeconds)
	fmt.Fprintf(w, "ModelFile Limits\t%d entries / %d bytes\n", cfg.ModelFileMaxEntries, cfg.ModelFileMaxBytes)
	fmt.Fprintf(w, "Request Delay\t%d ms\n", cfg.RequestDelayMS)
	fmt.Fprintf(w, "HTTP Timeout\t%d s\n", cfg.HTTPTimeoutSeconds)
	fmt.Fprintf(w, "Min Risk/Reward\t%.2f\n", cfg.MinRiskReward)
	fmt.Fprintf(w, "Min Volume\t%.0f\n", cfg.MinQuoteVolume)
	fmt.Fprintf(w, "Min Trade Count\t%d\n", cfg.MinTradeCount)
	fmt.Fprintf(w, "TLS Verify Disabled\t%t\n", cfg.InsecureSkipTLSVerify)
	return w.Flush()
}

func runTradeCommand(ctx context.Context, file *os.File, cfg config.Config, reviewer *brain.Client, sources []source.Source, side string, args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("usage: stonks %s SYMBOL QTY PRICE", side)
	}
	symbol := strings.ToUpper(strings.TrimSpace(args[0]))
	qty, err := strconv.ParseFloat(args[1], 64)
	if err != nil {
		return fmt.Errorf("invalid quantity: %w", err)
	}
	price, err := strconv.ParseFloat(args[2], 64)
	if err != nil {
		return fmt.Errorf("invalid price: %w", err)
	}
	note := ""
	if len(args) > 3 {
		note = strings.Join(args[3:], " ")
	}
	entry, err := trades.Record(cfg.TradeLogPath, side, symbol, qty, price, note, time.Now())
	if err != nil {
		return err
	}
	quotes, _ := latestQuotes(ctx, sources)
	summary, _ := loadTradeSummary(cfg.TradeLogPath, quotes)

	printBanner(file, "STONKS TRADES", colorCyan)
	fmt.Fprintln(file, "")
	fmt.Fprintf(file, "%s %s %s x %.2f @ %.2f\n",
		labelPill(strings.ToUpper(side), map[string]string{"buy": colorGreen, "sell": colorRed}[side]),
		style(entry.Symbol, colorBold, colorWhite),
		style("recorded", colorGray),
		entry.Quantity,
		entry.Price,
	)
	position := findPosition(summary.Positions, symbol)
	var aiReview *brain.Review
	if reviewer != nil {
		aiReview, _ = reviewer.ReviewTrade(ctx, side, entry, position, summary)
		if side == "sell" {
			if closed := latestClosedTrade(summary.ClosedTrades, symbol); closed != nil {
				_ = reviewer.RememberFact(fmt.Sprintf(
					"closed trade %s qty=%.2f avg_buy=%.2f sell=%.2f pnl=%.2f pnl_pct=%.2f",
					closed.Symbol,
					closed.Quantity,
					closed.AverageBuy,
					closed.SellPrice,
					closed.RealizedPnL,
					closed.RealizedPct,
				))
			}
		}
	}
	if position != nil {
		fmt.Fprintln(file, "")
		printSection(file, "POSITION", colorGreen)
		fmt.Fprintf(file, "  %s\n", compactStats([]string{
			style("qty ", colorGray) + style(fmt.Sprintf("%.2f", position.Quantity), colorWhite),
			style("avg ", colorGray) + style(fmt.Sprintf("%.2f", position.AverageCost), colorCyan),
			style("last ", colorGray) + style(fmt.Sprintf("%.2f", position.MarketPrice), colorYellow),
			style("uPnL ", colorGray) + style(fmt.Sprintf("%.2f", position.UnrealizedPnL), pnlColor(position.UnrealizedPnL)),
			style("uPnL%% ", colorGray) + style(fmt.Sprintf("%.2f%%", position.UnrealizedPct), pnlColor(position.UnrealizedPnL)),
		}))
	}
	if aiReview != nil {
		fmt.Fprintln(file, "")
		renderAIReview(file, "LOCAL AI REVIEW", aiReview)
	}
	return nil
}

func latestClosedTrade(items []trades.ClosedTrade, symbol string) *trades.ClosedTrade {
	for i := len(items) - 1; i >= 0; i-- {
		if strings.EqualFold(items[i].Symbol, symbol) {
			return &items[i]
		}
	}
	return nil
}

func runTradeReport(ctx context.Context, file *os.File, cfg config.Config, sources []source.Source, command string) error {
	quotes, _ := latestQuotes(ctx, sources)
	summary, err := loadTradeSummary(cfg.TradeLogPath, quotes)
	if err != nil {
		return err
	}
	switch command {
	case "portfolio":
		return renderPortfolio(file, cfg, summary)
	case "trades":
		return renderTrades(file, cfg, summary)
	default:
		return renderPnL(file, cfg, summary)
	}
}

func latestQuotes(ctx context.Context, sources []source.Source) (map[string]float64, error) {
	quotes := make(map[string]float64)
	for _, src := range sources {
		snapshot, err := src.Fetch(ctx)
		if err != nil {
			continue
		}
		for _, quote := range snapshot.Quotes {
			if quote.Symbol != "" && quote.LastPrice > 0 {
				quotes[quote.Symbol] = quote.LastPrice
			}
		}
	}
	if len(quotes) == 0 {
		return quotes, fmt.Errorf("no live quotes available")
	}
	return quotes, nil
}

func loadTradeSummary(path string, quotes map[string]float64) (trades.Summary, error) {
	entries, err := trades.Load(path)
	if err != nil {
		return trades.Summary{}, err
	}
	return trades.Summarize(entries, quotes), nil
}

func renderPortfolio(file *os.File, cfg config.Config, summary trades.Summary) error {
	printBanner(file, "STONKS PORTFOLIO", colorCyan)
	fmt.Fprintln(file, "")
	if len(summary.Positions) == 0 {
		fmt.Fprintln(file, "No open positions recorded yet.")
		return nil
	}
	fmt.Fprintln(file, style("SYMBOL     | QTY        | AVG COST    | LAST        | UPNL        | UPNL%", colorBold))
	fmt.Fprintln(file, style(strings.Repeat("─", 78), colorGray))
	for _, item := range summary.Positions {
		fmt.Fprintf(file, "%s | %s | %s | %s | %s | %s\n",
			padText(style(item.Symbol, colorBold, colorWhite), 10),
			padText(style(fmt.Sprintf("%.2f", item.Quantity), colorWhite), 10),
			padText(style(fmt.Sprintf("%.2f", item.AverageCost), colorCyan), 11),
			padText(style(fmt.Sprintf("%.2f", item.MarketPrice), colorYellow), 11),
			padText(style(fmt.Sprintf("%.2f", item.UnrealizedPnL), pnlColor(item.UnrealizedPnL)), 11),
			style(fmt.Sprintf("%.2f%%", item.UnrealizedPct), pnlColor(item.UnrealizedPnL)),
		)
	}
	fmt.Fprintln(file, "")
	fmt.Fprintf(file, "%s\n", compactStats([]string{
		style("invested ", colorGray) + style(fmt.Sprintf("%.2f", summary.InvestedCapital), colorWhite),
		style("market ", colorGray) + style(fmt.Sprintf("%.2f", summary.MarketValue), colorWhite),
		style("uPnL ", colorGray) + style(fmt.Sprintf("%.2f", summary.UnrealizedPnL), pnlColor(summary.UnrealizedPnL)),
	}))
	return nil
}

func renderTrades(file *os.File, cfg config.Config, summary trades.Summary) error {
	printBanner(file, "STONKS TRADES", colorCyan)
	fmt.Fprintln(file, "")
	if len(summary.Entries) == 0 {
		fmt.Fprintln(file, "No trade entries recorded yet.")
		return nil
	}
	limit := len(summary.Entries)
	if limit > 12 {
		limit = 12
	}
	fmt.Fprintln(file, style("TIME                      | SIDE | SYMBOL     | QTY        | PRICE", colorBold))
	fmt.Fprintln(file, style(strings.Repeat("─", 74), colorGray))
	for _, entry := range summary.Entries[len(summary.Entries)-limit:] {
		sideColor := colorGreen
		if entry.Side == "sell" {
			sideColor = colorRed
		}
		fmt.Fprintf(file, "%s | %s | %s | %s | %s\n",
			padText(style(entry.RecordedAt.Local().Format(time.RFC3339), colorGray), 25),
			padText(style(strings.ToUpper(entry.Side), sideColor), 4),
			padText(style(entry.Symbol, colorBold, colorWhite), 10),
			padText(style(fmt.Sprintf("%.2f", entry.Quantity), colorWhite), 10),
			style(fmt.Sprintf("%.2f", entry.Price), colorCyan),
		)
	}
	return nil
}

func renderPnL(file *os.File, cfg config.Config, summary trades.Summary) error {
	printBanner(file, "STONKS PNL", colorCyan)
	fmt.Fprintln(file, "")
	fmt.Fprintf(file, "%s\n", compactStats([]string{
		style("realized ", colorGray) + style(fmt.Sprintf("%.2f", summary.RealizedPnL), pnlColor(summary.RealizedPnL)),
		style("unrealized ", colorGray) + style(fmt.Sprintf("%.2f", summary.UnrealizedPnL), pnlColor(summary.UnrealizedPnL)),
		style("open positions ", colorGray) + style(fmt.Sprintf("%d", summary.OpenPositions), colorWhite),
	}))
	if len(summary.ClosedTrades) == 0 {
		fmt.Fprintln(file, "")
		fmt.Fprintln(file, "No closed trades recorded yet.")
		return nil
	}
	fmt.Fprintln(file, "")
	printSection(file, "RECENT CLOSED TRADES", colorYellow)
	limit := len(summary.ClosedTrades)
	if limit > 10 {
		limit = 10
	}
	fmt.Fprintln(file, style("SYMBOL     | QTY        | AVG BUY     | SELL        | PNL         | PNL%", colorBold))
	fmt.Fprintln(file, style(strings.Repeat("─", 76), colorGray))
	for _, item := range summary.ClosedTrades[len(summary.ClosedTrades)-limit:] {
		fmt.Fprintf(file, "%s | %s | %s | %s | %s | %s\n",
			padText(style(item.Symbol, colorBold, colorWhite), 10),
			padText(style(fmt.Sprintf("%.2f", item.Quantity), colorWhite), 10),
			padText(style(fmt.Sprintf("%.2f", item.AverageBuy), colorCyan), 11),
			padText(style(fmt.Sprintf("%.2f", item.SellPrice), colorYellow), 11),
			padText(style(fmt.Sprintf("%.2f", item.RealizedPnL), pnlColor(item.RealizedPnL)), 11),
			style(fmt.Sprintf("%.2f%%", item.RealizedPct), pnlColor(item.RealizedPnL)),
		)
	}
	return nil
}

func findPosition(items []trades.Position, symbol string) *trades.Position {
	for i := range items {
		if strings.EqualFold(items[i].Symbol, symbol) {
			return &items[i]
		}
	}
	return nil
}

func runPerformance(file *os.File, cfg config.Config) error {
	printBanner(file, "STONKS PERFORMANCE", colorCyan)
	fmt.Fprintln(file, "")

	perf, err := journal.PerformanceReport(cfg.JournalPath)
	if err != nil {
		return err
	}
	summary, err := journal.RebuildSummary(cfg.JournalPath, cfg.PerformanceSummaryPath)
	if err != nil {
		return err
	}
	if perf == nil && summary == nil {
		fmt.Fprintln(file, "Not enough journal history yet. Run more analyses on different sessions first.")
		return nil
	}

	if perf != nil {
		printSection(file, "RECENT WINDOW", colorCyan)
		w := new(tabwriter.Writer)
		w.Init(file, 0, 4, 2, ' ', 0)
		fmt.Fprintf(w, "Journal Path\t%s\n", cfg.JournalPath)
		fmt.Fprintf(w, "Runs Compared\t%d\n", perf.EntriesConsidered)
		fmt.Fprintf(w, "Picks Evaluated\t%d\n", perf.PicksEvaluated)
		fmt.Fprintf(w, "Win Rate\t%.2f%%\n", perf.WinRatePct)
		fmt.Fprintf(w, "Take Profit Rate\t%.2f%%\n", perf.TakeProfitRatePct)
		fmt.Fprintf(w, "Stop Loss Rate\t%.2f%%\n", perf.StopLossRatePct)
		fmt.Fprintf(w, "Average Change\t%.2f%%\n", perf.AverageChangePct)
		fmt.Fprintf(w, "Median Change\t%.2f%%\n", perf.MedianChangePct)
		if !perf.LatestReviewedRunAt.IsZero() {
			fmt.Fprintf(w, "Latest Reviewed Run\t%s\n", perf.LatestReviewedRunAt.Local().Format(time.RFC3339))
		}
		_ = w.Flush()
	}

	if summary != nil {
		fmt.Fprintln(file, "")
		printSection(file, "ALL-TIME SUMMARY", colorGreen)
		w := new(tabwriter.Writer)
		w.Init(file, 0, 4, 2, ' ', 0)
		fmt.Fprintf(w, "Summary Path\t%s\n", cfg.PerformanceSummaryPath)
		fmt.Fprintf(w, "Runs Compared\t%d\n", summary.RunsCompared)
		fmt.Fprintf(w, "Picks Evaluated\t%d\n", summary.PicksEvaluated)
		fmt.Fprintf(w, "Win Rate\t%.2f%%\n", summary.WinRatePct)
		fmt.Fprintf(w, "Take Profit Rate\t%.2f%%\n", summary.TakeProfitRatePct)
		fmt.Fprintf(w, "Stop Loss Rate\t%.2f%%\n", summary.StopLossRatePct)
		fmt.Fprintf(w, "Average Change\t%.2f%%\n", summary.AverageChangePct)
		if !summary.LastReviewedRunAt.IsZero() {
			fmt.Fprintf(w, "Last Updated From Run\t%s\n", summary.LastReviewedRunAt.Local().Format(time.RFC3339))
		}
		_ = w.Flush()
	}

	bestPick := (*journal.ReviewedPick)(nil)
	if summary != nil && summary.BestPick != nil {
		bestPick = summary.BestPick
	} else if perf != nil {
		bestPick = perf.BestPick
	}
	if bestPick != nil {
		fmt.Fprintln(file, "")
		printSection(file, "BEST HISTORICAL PICK", colorGreen)
		fmt.Fprintf(file, "  %s  Buy %.2f  Current %.2f  Change %.2f%%  Status %s\n",
			style(bestPick.Symbol, colorBold, colorWhite),
			bestPick.BuyPrice,
			bestPick.CurrentPrice,
			bestPick.ChangePct,
			colorReviewStatus(bestPick.Status),
		)
	}
	worstPick := (*journal.ReviewedPick)(nil)
	if summary != nil && summary.WorstPick != nil {
		worstPick = summary.WorstPick
	} else if perf != nil {
		worstPick = perf.WorstPick
	}
	if worstPick != nil {
		fmt.Fprintln(file, "")
		printSection(file, "WORST HISTORICAL PICK", colorRed)
		fmt.Fprintf(file, "  %s  Buy %.2f  Current %.2f  Change %.2f%%  Status %s\n",
			style(worstPick.Symbol, colorBold, colorWhite),
			worstPick.BuyPrice,
			worstPick.CurrentPrice,
			worstPick.ChangePct,
			colorReviewStatus(worstPick.Status),
		)
	}

	if perf != nil {
		fmt.Fprintln(file, "")
		printSection(file, "RECENT HISTORY", colorYellow)
		limit := len(perf.Items)
		if limit > 10 {
			limit = 10
		}
		fmt.Fprintln(file, style("SYMBOL     | BUY         | CURRENT     | CHANGE%     | STATUS", colorBold))
		fmt.Fprintln(file, style(strings.Repeat("─", 66), colorGray))
		for _, item := range perf.Items[len(perf.Items)-limit:] {
			fmt.Fprintf(file, "%s | %s | %s | %s | %s\n",
				padText(style(item.Symbol, colorBold, colorWhite), 10),
				padText(style(fmt.Sprintf("%.2f", item.BuyPrice), colorCyan), 11),
				padText(style(fmt.Sprintf("%.2f", item.CurrentPrice), reviewPriceColor(item.Status)), 11),
				padText(style(fmt.Sprintf("%.2f", item.ChangePct), reviewPriceColor(item.Status)), 11),
				colorReviewStatus(item.Status),
			)
		}
	}

	return nil
}

func runResetData(file *os.File, cfg config.Config, args []string) error {
	scope := "all"
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		scope = strings.ToLower(strings.TrimSpace(args[0]))
	}

	paths := make([]string, 0, 2)
	switch scope {
	case "all":
		paths = append(paths, cfg.JournalPath, cfg.TradeLogPath, cfg.ModelFilePath, cfg.PerformanceSummaryPath)
	case "journal", "logs":
		paths = append(paths, cfg.JournalPath, cfg.PerformanceSummaryPath)
	case "trades", "trade":
		paths = append(paths, cfg.TradeLogPath)
	case "brain", "model", "modelfile":
		paths = append(paths, cfg.ModelFilePath)
	case "performance", "summary":
		paths = append(paths, cfg.PerformanceSummaryPath)
	default:
		return fmt.Errorf("usage: stonks clear [journal|trades|brain|performance|all]")
	}

	removed := make([]string, 0, len(paths))
	missing := make([]string, 0, len(paths))
	for _, path := range paths {
		absPath, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		if err := os.Remove(absPath); err != nil {
			if os.IsNotExist(err) {
				missing = append(missing, absPath)
				continue
			}
			return err
		}
		removed = append(removed, absPath)
	}

	printBanner(file, "STONKS CLEAR", colorCyan)
	fmt.Fprintln(file, "")
	if len(removed) > 0 {
		printSection(file, "REMOVED", colorGreen)
		for _, item := range removed {
			fmt.Fprintf(file, "  %s\n", style(item, colorWhite))
		}
	}
	if len(missing) > 0 {
		fmt.Fprintln(file, "")
		printSection(file, "ALREADY EMPTY", colorYellow)
		for _, item := range missing {
			fmt.Fprintf(file, "  %s\n", style(item, colorGray))
		}
	}
	if len(removed) == 0 && len(missing) == 0 {
		fmt.Fprintln(file, "Nothing to clear.")
	}
	return nil
}

func renderAnalyzeResponse(file *os.File, response analyzeResponse) error {
	printBanner(file, "STONKS PICKS", colorCyan)
	fmt.Fprintln(file, "")
	fmt.Fprintf(file, "%s  %s  %s\n",
		labelPill("STATUS", colorGray),
		statusPill(response.MarketStatus),
		labelPill(strings.ToUpper(response.Mode), colorMagenta),
	)
	fmt.Fprintf(file, "%s %s\n", style("Summary", colorBold, colorWhite), style("·", colorGray)+" "+response.Summary)

	if response.BestPick != nil {
		best := response.BestPick
		fmt.Fprintln(file, "")
		printSection(file, "BEST PICK", colorGreen)
		fmt.Fprintf(file, "  %s  %s  %s\n", style(best.Symbol, colorBold, colorWhite), verdictPill(best.Verdict), scoreBadge(best.Confidence))
		if best.Verdict == "buy" {
			fmt.Fprintf(file, "  %s  %s  %s  %s\n",
				pricePill(best.BuyPrice, colorCyan),
				pricePill(best.StopLoss, colorRed),
				pricePill(best.TakeProfit, colorGreen),
				style(best.RiskRewardRatio, colorBold, colorYellow))
		} else {
			fmt.Fprintf(file, "  %s\n", compactStats([]string{
				style("spot ", colorGray) + style(fmt.Sprintf("%.2f", best.BuyPrice), colorCyan),
				style("watch stop ", colorGray) + style(fmt.Sprintf("%.2f", best.StopLoss), colorRed),
				style("watch target ", colorGray) + style(fmt.Sprintf("%.2f", best.TakeProfit), colorGreen),
				style("rr ", colorGray) + style(best.RiskRewardRatio, colorYellow),
			}))
			if best.Verdict == "watchlist" {
				fmt.Fprintf(file, "  %s\n", style("No entry is recommended yet. Keep this on watch only.", colorYellow))
			} else {
				fmt.Fprintf(file, "  %s\n", style("Avoid taking a fresh entry on this setup for now.", colorRed))
			}
		}
		fmt.Fprintf(file, "  %s\n", compactStats([]string{
			style("source ", colorGray) + style(best.Source, colorGray),
			style("momentum ", colorGray) + scoreBar(best.MomentumScore, colorBlue),
			style("liquidity ", colorGray) + scoreBar(best.LiquidityScore, colorCyan),
			style("flow ", colorGray) + scoreBar(best.FloorsheetScore, colorMagenta),
			style("tech ", colorGray) + scoreBar(best.IndicatorScore, colorGreen),
		}))
		if best.FundamentalsLoaded {
			fmt.Fprintf(file, "  %s\n", compactStats([]string{
				style("fund ", colorGray) + scoreBar(best.FundamentalScore, colorYellow),
				style("PE ", colorGray) + style(fmt.Sprintf("%.2f", best.PE), colorYellow),
				style("PB ", colorGray) + style(fmt.Sprintf("%.2f", best.PB), colorYellow),
				style("ROE ", colorGray) + style(fmt.Sprintf("%.2f", best.ROE), colorCyan),
				style("EPS ", colorGray) + style(fmt.Sprintf("%.2f", best.EPS), colorGreen),
				style("BVPS ", colorGray) + style(fmt.Sprintf("%.2f", best.BVPS), colorWhite),
			}))
		} else {
			fmt.Fprintf(file, "  %s\n", compactStats([]string{
				style("fund ", colorGray) + style("N/A", colorGray),
			}))
		}
		if best.IndicatorsLoaded {
			fmt.Fprintf(file, "  %s\n", compactStats([]string{
				style("RSI14 ", colorGray) + style(fmt.Sprintf("%.2f", best.RSI14), colorYellow),
				style("EMA20 ", colorGray) + style(fmt.Sprintf("%.2f", best.EMA20), colorWhite),
				style("EMA50 ", colorGray) + style(fmt.Sprintf("%.2f", best.EMA50), colorWhite),
				style("ATR14 ", colorGray) + style(fmt.Sprintf("%.2f", best.ATR14), colorBlue),
			}))
		} else {
			fmt.Fprintf(file, "  %s\n", compactStats([]string{
				style("RSI14 ", colorGray) + style("N/A", colorGray),
				style("EMA20 ", colorGray) + style("N/A", colorGray),
				style("EMA50 ", colorGray) + style("N/A", colorGray),
				style("ATR14 ", colorGray) + style("N/A", colorGray),
			}))
		}
		fmt.Fprintln(file, "")
		for _, reason := range best.Reasoning {
			fmt.Fprintf(file, "  %s %s\n", style("›", colorGray), reason)
		}
	}

	if len(response.Recommendations) > 0 {
		fmt.Fprintln(file, "")
		printSection(file, "TOP SETUPS", colorGreen)
		fmt.Fprintln(file, style("RK | SYMBOL     | VERDICT   | BUY        | STOP       | TARGET     | R:R    | CONF  | FUND  | RSI14 ", colorBold))
		fmt.Fprintln(file, style(strings.Repeat("─", 110), colorGray))
		for i, rec := range response.Recommendations {
			fundValue := style("N/A", colorGray)
			if rec.FundamentalsLoaded {
				fundValue = style(fmt.Sprintf("%.2f", rec.FundamentalScore), colorYellow)
			}
			rsiValue := style("N/A", colorGray)
			if rec.IndicatorsLoaded {
				rsiValue = style(fmt.Sprintf("%.2f", rec.RSI14), colorMagenta)
			}
			row := []string{
				padText(style(fmt.Sprintf("#%d", i+1), colorGray), 2),
				padText(style(rec.Symbol, colorBold, colorWhite), 10),
				padText(verdictCell(rec.Verdict), 9),
				padText(style(fmt.Sprintf("%.2f", rec.BuyPrice), colorCyan), 10),
				padText(style(fmt.Sprintf("%.2f", rec.StopLoss), colorRed), 10),
				padText(style(fmt.Sprintf("%.2f", rec.TakeProfit), colorGreen), 10),
				padText(style(rec.RiskRewardRatio, colorYellow), 6),
				padText(style(fmt.Sprintf("%.2f", rec.Confidence), colorBlue), 5),
				padText(fundValue, 5),
				padText(rsiValue, 5),
			}
			fmt.Fprintf(file, "%s | %s | %s | %s | %s | %s | %s | %s | %s | %s\n",
				row[0], row[1], row[2], row[3], row[4], row[5], row[6], row[7], row[8], row[9])
		}
	}

	if response.Review != nil {
		fmt.Fprintln(file, "")
		printSection(file, "REVIEW", colorCyan)
		fmt.Fprintf(file, "  %s\n", response.Review.Summary)
		if !response.Review.PreviousRunAt.IsZero() {
			fmt.Fprintf(file, "  Previous Run: %s\n", response.Review.PreviousRunAt.Local().Format(time.RFC3339))
		}
		if len(response.Review.Items) > 0 {
			fmt.Fprintln(file, style("SYMBOL     | CURRENT     | CHANGE%     | STATUS", colorBold))
			fmt.Fprintln(file, style(strings.Repeat("─", 58), colorGray))
			for _, item := range response.Review.Items {
				fmt.Fprintf(file, "%s | %s | %s | %s\n",
					padText(style(item.Symbol, colorBold, colorWhite), 10),
					padText(style(fmt.Sprintf("%.2f", item.CurrentPrice), reviewPriceColor(item.Status)), 11),
					padText(style(fmt.Sprintf("%.2f", item.ChangePct), reviewPriceColor(item.Status)), 11),
					colorReviewStatus(item.Status),
				)
			}
		}
	}

	if response.AIReview != nil {
		fmt.Fprintln(file, "")
		renderAIReview(file, "LOCAL AI REVIEW", response.AIReview)
	}

	if len(response.DiscardedSymbols) > 0 {
		fmt.Fprintln(file, "")
		printSection(file, "REJECTED SYMBOLS", colorYellow)
		limit := len(response.DiscardedSymbols)
		if limit > 12 {
			limit = 12
		}
		for _, item := range response.DiscardedSymbols[:limit] {
			fmt.Fprintf(file, "  - %s: %s\n", style(item.Symbol, colorYellow), item.Reason)
		}
		if len(response.DiscardedSymbols) > limit {
			fmt.Fprintf(file, "  ... %d more\n", len(response.DiscardedSymbols)-limit)
		}
	}

	return nil
}

func renderAIReview(file *os.File, title string, review *brain.Review) {
	printSection(file, title, colorBlue)
	if strings.TrimSpace(review.Verdict) != "" {
		fmt.Fprintf(file, "  %s %s\n", style("verdict", colorGray), verdictPill(review.Verdict))
	}
	if strings.TrimSpace(review.Stance) != "" {
		fmt.Fprintf(file, "  %s %s\n", style("stance", colorGray), aiStance(review.Stance))
	}
	if strings.TrimSpace(review.Risk) != "" {
		fmt.Fprintf(file, "  %s %s\n", style("risk", colorGray), review.Risk)
	}
	if strings.TrimSpace(review.Note) != "" {
		fmt.Fprintf(file, "  %s %s\n", style("note", colorGray), review.Note)
	}
	if strings.TrimSpace(review.Lesson) != "" {
		fmt.Fprintf(file, "  %s %s\n", style("lesson", colorGray), review.Lesson)
	}
}

func printBanner(file *os.File, title, color string) {
	const width = 72
	line := strings.Repeat("═", width)
	inner := "  " + title + "  "
	padding := (width - visibleWidth(inner)) / 2
	if padding < 0 {
		padding = 0
	}
	centered := strings.Repeat(" ", padding) + inner
	fmt.Fprintln(file, style(line, color))
	fmt.Fprintln(file, style(centered, colorBold, colorWhite))
	fmt.Fprintln(file, style(line, color))
}

func printSection(file *os.File, title, color string) {
	line := strings.Repeat("─", max(8, 58-visibleWidth(title)))
	fmt.Fprintln(file, style("▌ "+title+" ", colorBold, color)+style(line, colorGray))
}

func style(value string, codes ...string) string {
	return strings.Join(codes, "") + value + colorReset
}

func colorStatus(status string) string {
	switch status {
	case "ok":
		return style(status, colorGreen, colorBold)
	case "fallback_candidates":
		return style(status, colorYellow, colorBold)
	default:
		return style(status, colorRed, colorBold)
	}
}

func colorReviewStatus(status string) string {
	switch status {
	case "take_profit_hit":
		return style(status, colorGreen, colorBold)
	case "stop_loss_hit":
		return style(status, colorRed, colorBold)
	case "above_buy":
		return style(status, colorCyan)
	default:
		return style(status, colorYellow)
	}
}

func reviewPriceColor(status string) string {
	switch status {
	case "take_profit_hit", "above_buy":
		return colorGreen
	case "stop_loss_hit", "below_buy":
		return colorRed
	default:
		return colorYellow
	}
}

func pnlColor(value float64) string {
	switch {
	case value > 0:
		return colorGreen
	case value < 0:
		return colorRed
	default:
		return colorYellow
	}
}

func padText(value string, width int) string {
	plain := visibleWidth(value)
	if plain >= width {
		return value
	}
	return value + strings.Repeat(" ", width-plain)
}

func visibleWidth(value string) int {
	width := 0
	inANSI := false
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if inANSI {
			if ch == 'm' {
				inANSI = false
			}
			continue
		}
		if ch == 0x1b && i+1 < len(value) && value[i+1] == '[' {
			inANSI = true
			continue
		}
		width++
	}
	return width
}

func pricePill(value float64, color string) string {
	return style("["+fmt.Sprintf("%.2f", value)+"]", colorBold, color)
}

func scoreBadge(score float64) string {
	return labelPill(fmt.Sprintf("CONF %.2f", score), colorBlue)
}

func scoreBar(score float64, color string) string {
	blocks := int(score*8 + 0.5)
	if blocks < 0 {
		blocks = 0
	}
	if blocks > 8 {
		blocks = 8
	}
	bar := strings.Repeat("█", blocks) + strings.Repeat("░", 8-blocks)
	return style(bar, color) + " " + style(fmt.Sprintf("%.2f", score), colorBold)
}

func labelPill(value, color string) string {
	return style("["+value+"]", colorBold, color)
}

func statusPill(status string) string {
	switch status {
	case "ok":
		return labelPill("OK", colorGreen)
	case "fallback_candidates":
		return labelPill("FALLBACK", colorYellow)
	default:
		return labelPill("NO PICKS", colorRed)
	}
}

func verdictPill(verdict string) string {
	switch strings.ToLower(strings.TrimSpace(verdict)) {
	case "buy":
		return labelPill("BUY", colorGreen)
	case "avoid":
		return labelPill("AVOID", colorRed)
	default:
		return labelPill("WATCHLIST", colorYellow)
	}
}

func verdictCell(verdict string) string {
	switch strings.ToLower(strings.TrimSpace(verdict)) {
	case "buy":
		return style("BUY", colorGreen, colorBold)
	case "avoid":
		return style("AVOID", colorRed, colorBold)
	default:
		return style("WATCH", colorYellow, colorBold)
	}
}

func compactStats(parts []string) string {
	return strings.Join(parts, "   ")
}

func aiStance(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "bullish":
		return style(value, colorGreen, colorBold)
	case "bearish":
		return style(value, colorRed, colorBold)
	default:
		return style(value, colorYellow, colorBold)
	}
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
