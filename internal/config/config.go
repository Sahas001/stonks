package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	ConfigDir              string
	EnvPath                string
	ModelFilePath          string
	PerformanceSummaryPath string
	Port                   string
	TopN                   int
	MinRiskReward          float64
	MaxRiskPerTradePct     float64
	DefaultTakeProfitRR    float64
	MinQuoteVolume         float64
	MinTradeCount          int
	CandleShortlistSize    int
	InactiveLookbackDays   int
	InactiveMinTradeDays   int
	JournalPath            string
	TradeLogPath           string
	JournalRetentionDays   int
	JournalMaxEntries      int
	PickAppendMinutes      int
	AIReviewEnabled        bool
	AIReviewCommand        string
	AIReviewTimeoutSeconds int
	ModelFileMaxEntries    int
	ModelFileMaxBytes      int
	HTTPTimeoutSeconds     int
	HTTPUserAgent          string
	HTTPReferer            string
	RequestDelayMS         int
	InsecureSkipTLSVerify  bool
	NepseBaseURL           string
	NepseQuotePath         string
	NepseFloorsheetPath    string
	NepseAuthToken         string
	NepseHeaders           map[string]string
	AlphaBaseURL           string
	AlphaForceURLKeyPath   string
	AlphaForceURLKeyFS     string
	AlphaQuotePath         string
	AlphaFloorsheetPath    string
	AlphaHistoryPath       string
	AlphaProfilePath       string
	HistoryResolution      string
	HistoryFrame           int
	AlphaToken             string
	AlphaHeaders           map[string]string
	AlphaCookieName        string
	AlphaCookieValue       string
	EnabledSources         []string
}

func Load() Config {
	configDir := defaultConfigDir()
	envPath := filepath.Join(configDir, ".env")
	loadDotEnv(envPath)
	loadDotEnv(".env")
	journalPath := filepath.Join(configDir, "journal.jsonl")
	tradeLogPath := filepath.Join(configDir, "trades.jsonl")
	modelFilePath := filepath.Join(configDir, "modelfile")
	performanceSummaryPath := filepath.Join(configDir, "performance_summary.json")

	return Config{
		ConfigDir:              configDir,
		EnvPath:                envPath,
		ModelFilePath:          getenv("STONKS_MODELFILE_PATH", modelFilePath),
		PerformanceSummaryPath: getenv("STONKS_PERFORMANCE_SUMMARY_PATH", performanceSummaryPath),
		Port:                   getenv("STONKS_PORT", "8080"),
		TopN:                   getenvInt("STONKS_TOP_N", 5),
		MinRiskReward:          getenvFloat("STONKS_MIN_RISK_REWARD", 1.8),
		MaxRiskPerTradePct:     getenvFloat("STONKS_MAX_RISK_PCT", 3.0),
		DefaultTakeProfitRR:    getenvFloat("STONKS_DEFAULT_TP_RR", 2.2),
		MinQuoteVolume:         getenvFloat("STONKS_MIN_VOLUME", 1000),
		MinTradeCount:          getenvInt("STONKS_MIN_TRADE_COUNT", 8),
		CandleShortlistSize:    getenvInt("STONKS_CANDLE_SHORTLIST_SIZE", 12),
		InactiveLookbackDays:   getenvInt("STONKS_INACTIVE_LOOKBACK_DAYS", 120),
		InactiveMinTradeDays:   getenvInt("STONKS_INACTIVE_MIN_TRADE_DAYS", 5),
		JournalPath:            getenv("STONKS_JOURNAL_PATH", journalPath),
		TradeLogPath:           getenv("STONKS_TRADE_LOG_PATH", tradeLogPath),
		JournalRetentionDays:   getenvInt("STONKS_JOURNAL_RETENTION_DAYS", 14),
		JournalMaxEntries:      getenvInt("STONKS_JOURNAL_MAX_ENTRIES", 120),
		PickAppendMinutes:      getenvInt("STONKS_PICK_APPEND_MINUTES", 60),
		AIReviewEnabled:        getenvBool("STONKS_AI_REVIEW_ENABLED", true),
		AIReviewCommand:        getenv("STONKS_AI_REVIEW_COMMAND", "ollama run deepseek-r1:1.5b"),
		AIReviewTimeoutSeconds: getenvInt("STONKS_AI_REVIEW_TIMEOUT_SECONDS", 25),
		ModelFileMaxEntries:    getenvInt("STONKS_MODELFILE_MAX_ENTRIES", 18),
		ModelFileMaxBytes:      getenvInt("STONKS_MODELFILE_MAX_BYTES", 4096),
		HTTPTimeoutSeconds:     getenvInt("STONKS_HTTP_TIMEOUT_SECONDS", 20),
		HTTPUserAgent:          getenv("STONKS_HTTP_USER_AGENT", "stonks/0.1 (+personal research tool)"),
		HTTPReferer:            getenv("STONKS_HTTP_REFERER", "https://nepsealpha.com/"),
		RequestDelayMS:         getenvInt("STONKS_REQUEST_DELAY_MS", 1200),
		InsecureSkipTLSVerify:  getenvBool("STONKS_INSECURE_SKIP_TLS_VERIFY", false),
		NepseBaseURL:           getenv("STONKS_NEPSE_BASE_URL", "https://www.nepalstock.com"),
		NepseQuotePath:         getenv("STONKS_NEPSE_QUOTE_PATH", "/api/nots/marketwatch"),
		NepseFloorsheetPath:    getenv("STONKS_NEPSE_FLOORSHEET_PATH", "/api/nots/floorsheet"),
		NepseAuthToken:         os.Getenv("STONKS_NEPSE_AUTH_TOKEN"),
		NepseHeaders:           getenvMap("STONKS_NEPSE_HEADERS"),
		AlphaBaseURL:           getenv("STONKS_ALPHA_BASE_URL", "https://nepsealpha.com"),
		AlphaForceURLKeyPath:   getenv("STONKS_ALPHA_FORCE_URL_KEY_PATH", "/force-url-key"),
		AlphaForceURLKeyFS:     getenv("STONKS_ALPHA_FORCE_URL_KEY_FS", "161130"),
		AlphaQuotePath:         getenv("STONKS_ALPHA_QUOTE_PATH", "/sastoshare/get_live_market/1?fsk={{fsk}}&fs={{quote_fs}}"),
		AlphaFloorsheetPath:    getenv("STONKS_ALPHA_FLOORSHEET_PATH", "/floorsheet-live-today/filter?fsk={{fsk}}&page=1&lvs={{lvs}}&contractNumber=&stockSymbol=&buyer=&seller=&itemsPerPage=20"),
		AlphaHistoryPath:       getenv("STONKS_ALPHA_HISTORY_PATH", "/trading/1/history?fsk={{fsk}}&symbol={{symbol}}&resolution={{resolution}}&frame={{frame}}"),
		AlphaProfilePath:       getenv("STONKS_ALPHA_PROFILE_PATH", "/search?q={{symbol}}"),
		HistoryResolution:      getenv("STONKS_HISTORY_RESOLUTION", "1D"),
		HistoryFrame:           getenvInt("STONKS_HISTORY_FRAME", 300),
		AlphaToken:             os.Getenv("STONKS_ALPHA_TOKEN"),
		AlphaHeaders:           getenvMap("STONKS_ALPHA_HEADERS"),
		AlphaCookieName:        getenv("STONKS_ALPHA_COOKIE_NAME", ""),
		AlphaCookieValue:       os.Getenv("STONKS_ALPHA_COOKIE_VALUE"),
		EnabledSources:         getenvList("STONKS_SOURCES", []string{"nepse"}),
	}
}

func defaultConfigDir() string {
	dir, err := os.UserConfigDir()
	if err == nil && strings.TrimSpace(dir) != "" {
		return filepath.Join(dir, "stonks")
	}
	home, homeErr := os.UserHomeDir()
	if homeErr == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".config", "stonks")
	}
	return "."
}

func loadDotEnv(path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if key == "" {
			continue
		}

		value = strings.Trim(value, `"'`)
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		_ = os.Setenv(key, value)
	}
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func getenvFloat(key string, fallback float64) float64 {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fallback
	}
	return value
}

func getenvList(key string, fallback []string) []string {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}

func getenvMap(key string) map[string]string {
	raw := os.Getenv(key)
	if raw == "" {
		return nil
	}
	out := make(map[string]string)
	for _, pair := range strings.Split(raw, ";") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) != 2 {
			continue
		}
		k := strings.TrimSpace(parts[0])
		v := strings.TrimSpace(parts[1])
		if k != "" && v != "" {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func getenvBool(key string, fallback bool) bool {
	raw := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if raw == "" {
		return fallback
	}
	switch raw {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}
