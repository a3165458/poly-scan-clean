package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"poly-scan/internal/btc"
	"poly-scan/internal/cost"
	"poly-scan/internal/notification"
	"poly-scan/internal/polymarket"

	"github.com/gorilla/websocket"
)

// API Server for monitoring dashboard
type APIServer struct {
	client      *polymarket.Client
	spotMonitor *btc.SpotMonitor
	upgrader    websocket.Upgrader

	// State
	markets       []MarketInfo
	positions     []PositionInfo
	trades        []TradeInfo
	alerts        []AlertInfo
	strategyState StrategyState
	config        StrategyConfig

	mu        sync.RWMutex
	wsClients map[*websocket.Conn]bool
	wsMu      sync.RWMutex
	writeMu   sync.Mutex

	userAddress string
	stopChan    chan struct{}
}

type MarketInfo struct {
	ID          string    `json:"id"`
	Question    string    `json:"question"`
	UpTokenID   string    `json:"up_token_id"`
	DownTokenID string    `json:"down_token_id"`
	WindowStart time.Time `json:"window_start"`
	WindowEnd   time.Time `json:"window_end"`
	TimeLeft    string    `json:"time_left"`
	Status      string    `json:"status"` // "active", "ending_soon", "ended"
}

type PositionInfo struct {
	MarketID     string    `json:"market_id"`
	MarketName   string    `json:"market_name"`
	TokenID      string    `json:"token_id"`
	Side         string    `json:"side"`
	EntryPrice   float64   `json:"entry_price"`
	CurrentPrice float64   `json:"current_price"`
	Size         float64   `json:"size"`
	CurrentValue float64   `json:"current_value"`
	PnL          float64   `json:"pnl"`
	PnLPct       float64   `json:"pnl_pct"`
	OpenTime     time.Time `json:"open_time"`
	Duration     string    `json:"duration"`
	IsActive     bool      `json:"is_active"`
	CloseReason  string    `json:"close_reason"`
}

type TradeInfo struct {
	ID         string    `json:"id"`
	Timestamp  time.Time `json:"timestamp"`
	MarketID   string    `json:"market_id"`
	MarketName string    `json:"market_name"`
	Direction  string    `json:"direction"`
	Side       string    `json:"side"` // "BUY" or "SELL"
	Price      float64   `json:"price"`
	Size       float64   `json:"size"`
	Total      float64   `json:"total"`
	Confidence float64   `json:"confidence"`
	OrderID    string    `json:"order_id"`
	Status     string    `json:"status"` // "pending", "filled", "cancelled"
	PnL        float64   `json:"pnl"`
}

type AlertInfo struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`  // "trade", "risk", "system", "signal"
	Level     string    `json:"level"` // "info", "warning", "error", "success"
	Message   string    `json:"message"`
	Details   string    `json:"details,omitempty"`
}

type StrategyState struct {
	Name            string    `json:"name"`
	Status          string    `json:"status"` // "running", "stopped", "error"
	StartTime       time.Time `json:"start_time"`
	Uptime          string    `json:"uptime"`
	TotalTrades     int       `json:"total_trades"`
	WinningTrades   int       `json:"winning_trades"`
	LosingTrades    int       `json:"losing_trades"`
	WinRate         float64   `json:"win_rate"`
	TotalPnL        float64   `json:"total_pnl"`
	DailyPnL        float64   `json:"daily_pnl"`
	DailyTrades     int       `json:"daily_trades"`
	ConsecutiveWins int       `json:"consecutive_wins"`
	ConsecutiveLoss int       `json:"consecutive_loss"`
	Drawdown        float64   `json:"drawdown"`
	InCooldown      bool      `json:"in_cooldown"`
}

type StrategyConfig struct {
	MinConfidence      float64 `json:"min_confidence"`
	MinPriceChange     float64 `json:"min_price_change"`
	MaxPositionUSD     float64 `json:"max_position_usd"`
	PredictBeforeEnd   int     `json:"predict_before_end"`
	ExecutionLeadTime  int     `json:"execution_lead_time"`
	CooldownPerMarket  int     `json:"cooldown_per_market"`
	UseDynamicPricing  bool    `json:"use_dynamic_pricing"`
	PriceSlippage      float64 `json:"price_slippage"`
	EnableRiskMgmt     bool    `json:"enable_risk_mgmt"`
	MaxDailyLoss       float64 `json:"max_daily_loss"`
	MaxDrawdownPct     float64 `json:"max_drawdown_pct"`
	MaxConsecutiveLoss int     `json:"max_consecutive_loss"`
	MaxDailyTrades     int     `json:"max_daily_trades"`
}

func defaultConfig() StrategyConfig {
	return StrategyConfig{
		MinConfidence:      0.65,
		MinPriceChange:     0.02,
		MaxPositionUSD:     50.0,
		PredictBeforeEnd:   10,
		ExecutionLeadTime:  3,
		CooldownPerMarket:  60,
		UseDynamicPricing:  true,
		PriceSlippage:      0.03,
		EnableRiskMgmt:     true,
		MaxDailyLoss:       100.0,
		MaxDrawdownPct:     0.20,
		MaxConsecutiveLoss: 3,
		MaxDailyTrades:     20,
	}
}

func loadConfig() StrategyConfig {
	config := defaultConfig()
	data, err := os.ReadFile("data/config.json")
	if err != nil {
		return config
	}
	_ = json.Unmarshal(data, &config)
	return config
}

func saveConfig(config StrategyConfig) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile("data/config.json", data, 0644)
}

type TechnicalIndicators struct {
	SpotPrice     float64   `json:"spot_price"`
	RSI           float64   `json:"rsi"`
	MACD          float64   `json:"macd"`
	MACDSignal    float64   `json:"macd_signal"`
	MACDHist      float64   `json:"macd_hist"`
	BollingerUp   float64   `json:"bollinger_up"`
	BollingerMid  float64   `json:"bollinger_mid"`
	BollingerLow  float64   `json:"bollinger_low"`
	BollingerPct  float64   `json:"bollinger_pct"`
	ATR           float64   `json:"atr"`
	Momentum      float64   `json:"momentum"`
	Trend         string    `json:"trend"`
	TrendStrength float64   `json:"trend_strength"`
	Timestamp     time.Time `json:"timestamp"`
}

type PriceHistory struct {
	Prices []PricePoint `json:"prices"`
}

type PricePoint struct {
	Price     float64   `json:"price"`
	Timestamp time.Time `json:"timestamp"`
}

type DashboardData struct {
	Strategy     StrategyState         `json:"strategy"`
	Indicators   TechnicalIndicators   `json:"indicators"`
	Markets      []MarketInfo          `json:"markets"`
	Positions    []PositionInfo        `json:"positions"`
	RecentTrades []TradeInfo           `json:"recent_trades"`
	Alerts       []AlertInfo           `json:"alerts"`
	PriceHistory []PricePoint          `json:"price_history"`
	Performance  *btc.PerformanceStats `json:"performance,omitempty"`
}

func NewAPIServer() *APIServer {
	client := polymarket.NewClient()
	spotMonitor := btc.NewSpotMonitor()

	userAddress := os.Getenv("POLY_FUNDER_ADDRESS")
	if userAddress == "" {
		userAddress = os.Getenv("POLY_ADDRESS")
	}

	return &APIServer{
		client:      client,
		spotMonitor: spotMonitor,
		upgrader:    websocket.Upgrader{},
		markets:     make([]MarketInfo, 0),
		positions:   make([]PositionInfo, 0),
		trades:      make([]TradeInfo, 0),
		alerts:      make([]AlertInfo, 0),
		wsClients:   make(map[*websocket.Conn]bool),
		userAddress: userAddress,
		stopChan:    make(chan struct{}),
		config:      loadConfig(),
		strategyState: StrategyState{
			Name:      "BTC 5-Minute Delay Arbitrage",
			Status:    "running",
			StartTime: time.Now(),
		},
	}
}

func (s *APIServer) Start() error {
	// Start spot monitor
	s.spotMonitor.Start()

	// Start background data refresh
	go s.refreshDataLoop()
	go s.broadcastLoop()

	// Setup routes
	mux := http.NewServeMux()

	// API routes
	mux.HandleFunc("/api/dashboard", s.handleDashboard)
	mux.HandleFunc("/api/markets", s.handleMarkets)
	mux.HandleFunc("/api/positions", s.handlePositions)
	mux.HandleFunc("/api/trades", s.handleTrades)
	mux.HandleFunc("/api/alerts", s.handleAlerts)
	mux.HandleFunc("/api/indicators", s.handleIndicators)
	mux.HandleFunc("/api/price-history", s.handlePriceHistory)
	mux.HandleFunc("/api/strategy", s.handleStrategy)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/performance", s.handlePerformance)
	mux.HandleFunc("/api/account", s.handleAccount)
	mux.HandleFunc("/api/notifications", s.handleNotifications)
	mux.HandleFunc("/api/notifications/config", s.handleNotificationsConfig)
	mux.HandleFunc("/api/costs", s.handleCosts)
	mux.HandleFunc("/api/costs/daily", s.handleCostsDaily)
	mux.HandleFunc("/api/trading/stop", s.handleTradingStop)
	mux.HandleFunc("/ws", s.handleWebSocket)

	// CORS middleware
	handler := s.corsMiddleware(mux)

	port := os.Getenv("API_PORT")
	if port == "" {
		port = "9876"
	}

	// Security: bind to localhost only — use nginx reverse proxy for external access
	bindHost := os.Getenv("API_BIND_HOST")
	if bindHost == "" {
		bindHost = "127.0.0.1"
	}
	listenAddr := bindHost + ":" + port
	log.Printf("[API] Dashboard server starting on http://%s", listenAddr)

	srv := &http.Server{
		Addr:    listenAddr,
		Handler: handler,
	}

	// Graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan

		log.Println("[API] Shutting down...")
		close(s.stopChan)
		s.spotMonitor.Stop()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	return srv.ListenAndServe()
}

func (s *APIServer) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *APIServer) handleDashboard(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data := DashboardData{
		Strategy:     s.strategyState,
		Indicators:   s.getCurrentIndicators(),
		Markets:      s.markets,
		Positions:    s.positions,
		RecentTrades: s.getRecentTrades(20),
		Alerts:       s.getRecentAlerts(10),
		PriceHistory: s.getPriceHistory(100),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func (s *APIServer) handleMarkets(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.markets)
}

func (s *APIServer) handlePositions(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.positions)
}

func (s *APIServer) handleTrades(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	limit := 200
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.getRecentTrades(limit))
}

func (s *APIServer) handleAlerts(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.getRecentAlerts(50))
}

func (s *APIServer) handleIndicators(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.getCurrentIndicators())
}

func (s *APIServer) handlePriceHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.getPriceHistory(200))
}

func (s *APIServer) handleStrategy(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.strategyState.Uptime = time.Since(s.strategyState.StartTime).Round(time.Second).String()
	// Reflect file-based stop signal in status
	if _, err := os.Stat("data/trading_stopped"); err == nil {
		s.strategyState.Status = "stopped"
	} else if s.strategyState.Status == "stopped" {
		s.strategyState.Status = "running"
	}
	s.mu.Unlock()

	s.mu.RLock()
	defer s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.strategyState)
}

func (s *APIServer) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		s.mu.RLock()
		config := s.config
		s.mu.RUnlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(config)
		return
	}

	if r.Method == "POST" {
		var newConfig StrategyConfig
		if err := json.NewDecoder(r.Body).Decode(&newConfig); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err := saveConfig(newConfig); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		s.mu.Lock()
		s.config = newConfig
		s.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(newConfig)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (s *APIServer) handleTradingStop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method == "GET" {
		// Check if trading is stopped
		_, stopped := os.Stat("data/trading_stopped")
		json.NewEncoder(w).Encode(map[string]bool{"stopped": stopped == nil})
		return
	}

	if r.Method == "POST" {
		var req struct {
			Stopped bool `json:"stopped"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.Stopped {
			// Create stop signal file
			if err := os.WriteFile("data/trading_stopped", []byte(time.Now().Format(time.RFC3339)), 0644); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			log.Printf("[API] ⚠️ Trading STOPPED by dashboard user")
		} else {
			// Remove stop signal file
			os.Remove("data/trading_stopped")
			log.Printf("[API] ✅ Trading RESUMED by dashboard user")
		}

		json.NewEncoder(w).Encode(map[string]bool{"stopped": req.Stopped})
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (s *APIServer) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[WS] Upgrade error: %v", err)
		return
	}

	s.wsMu.Lock()
	s.wsClients[conn] = true
	s.wsMu.Unlock()

	log.Printf("[WS] Client connected. Total: %d", len(s.wsClients))

	// Send initial data
	s.sendWSMessage(conn, "dashboard", s.getDashboardData())

	// Keep connection alive and handle messages
	go func() {
		defer func() {
			s.wsMu.Lock()
			delete(s.wsClients, conn)
			s.wsMu.Unlock()
			conn.Close()
			log.Printf("[WS] Client disconnected. Total: %d", len(s.wsClients))
		}()

		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				break
			}
		}
	}()
}

func (s *APIServer) sendWSMessage(conn *websocket.Conn, msgType string, data interface{}) {
	msg := map[string]interface{}{
		"type":      msgType,
		"data":      data,
		"timestamp": time.Now(),
	}
	s.writeMu.Lock()
	conn.WriteJSON(msg)
	s.writeMu.Unlock()
}

func (s *APIServer) broadcast(msgType string, data interface{}) {
	s.wsMu.RLock()
	clients := make([]*websocket.Conn, 0, len(s.wsClients))
	for conn := range s.wsClients {
		clients = append(clients, conn)
	}
	s.wsMu.RUnlock()

	var failed []*websocket.Conn
	for _, conn := range clients {
		s.writeMu.Lock()
		err := conn.WriteJSON(map[string]interface{}{
			"type":      msgType,
			"data":      data,
			"timestamp": time.Now(),
		})
		s.writeMu.Unlock()
		if err != nil {
			conn.Close()
			failed = append(failed, conn)
		}
	}

	if len(failed) > 0 {
		s.wsMu.Lock()
		for _, conn := range failed {
			delete(s.wsClients, conn)
		}
		s.wsMu.Unlock()
	}
}

func (s *APIServer) broadcastLoop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			s.broadcast("indicators", s.getCurrentIndicators())
			s.broadcast("price_point", PricePoint{
				Price:     s.spotMonitor.GetCurrentPrice(),
				Timestamp: time.Now(),
			})
		}
	}
}

func (s *APIServer) refreshDataLoop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			s.refreshMarkets()
			s.refreshPositions()
			s.refreshTrades()
			s.refreshStrategyState()
		}
	}
}

func (s *APIServer) refreshTrades() {
	if s.userAddress == "" {
		return
	}

	// Load all data sources
	apiTrades, _ := s.client.GetTrades(s.userAddress, 200)

	perfResults := s.loadTradeResults()
	perfByMarket := make(map[string]btc.TradeResult)
	for _, tr := range perfResults {
		perfByMarket[tr.MarketID] = tr
	}

	positionsByMarket := s.loadPositions()

	// Track which markets have been processed
	processedMarkets := make(map[string]bool)

	var trades []TradeInfo

	// Primary source: positions.json (all positions our bot opened)
	for slug, pos := range positionsByMarket {
		processedMarkets[slug] = true

		pnl := 0.0
		confidence := pos.OriginalConf
		status := "CLOSED"
		entryPrice := pos.EntryPrice
		size := pos.Size

		if pos.IsActive {
			status = "ACTIVE"
		} else if perf, ok := perfByMarket[slug]; ok {
			// Use performance.json for accurate PnL
			pnl = perf.PnL
			confidence = perf.Confidence
			if perf.Size > 0 {
				size = perf.Size
			}
			if perf.Success {
				status = "WON"
			} else {
				status = "LOST"
			}
		}

		marketName := s.getMarketName(slug, apiTrades)

		trades = append(trades, TradeInfo{
			ID:         slug,
			Timestamp:  pos.OpenTime,
			MarketID:   slug,
			MarketName: marketName,
			Direction:  pos.Side,
			Side:       "BUY",
			Price:      entryPrice,
			Size:       size,
			Total:      entryPrice * size,
			Confidence: confidence,
			OrderID:    slug,
			Status:     status,
			PnL:        pnl,
		})
	}

	// Add performance-only trades not in positions.json (edge case)
	for _, tr := range perfResults {
		if processedMarkets[tr.MarketID] {
			continue
		}
		status := "LOST"
		if tr.Success {
			status = "WON"
		}
		trades = append(trades, TradeInfo{
			ID:         tr.MarketID,
			Timestamp:  tr.OpenTime,
			MarketID:   tr.MarketID,
			MarketName: tr.MarketID,
			Direction:  tr.Direction,
			Side:       "BUY",
			Price:      tr.EntryPrice,
			Size:       tr.Size,
			Total:      tr.EntryPrice * tr.Size,
			Confidence: tr.Confidence,
			OrderID:    tr.MarketID,
			Status:     status,
			PnL:        tr.PnL,
		})
	}

	sortTradesByTime(trades)

	s.mu.Lock()
	s.trades = trades
	s.mu.Unlock()
}

// getMarketName finds the human-readable market name from on-chain trades
func (s *APIServer) getMarketName(slug string, apiTrades []polymarket.Trade) string {
	for _, t := range apiTrades {
		if t.Slug == slug && t.Title != "" {
			return t.Title
		}
	}
	return slug
}

func sortTradesByTime(trades []TradeInfo) {
	for i := 0; i < len(trades)-1; i++ {
		for j := i + 1; j < len(trades); j++ {
			if trades[j].Timestamp.After(trades[i].Timestamp) {
				trades[i], trades[j] = trades[j], trades[i]
			}
		}
	}
}

func (s *APIServer) refreshMarkets() {
	// Try to get markets from Gamma API (for non-BTC markets)
	parsedMarkets, err := s.client.GetMarkets(50)
	if err != nil {
		log.Printf("[API] Failed to fetch markets: %v", err)
	}

	var markets []MarketInfo

	// Add BTC 5-minute markets (generated based on current time)
	btcMarkets := s.generateBTC5MinMarkets()
	markets = append(markets, btcMarkets...)

	// Add other markets from Gamma API
	for _, m := range parsedMarkets {
		if len(m.Tokens) != 2 {
			continue
		}

		// Skip if already added as BTC market
		if strings.Contains(m.Question, "Bitcoin") || strings.Contains(m.Question, "BTC") {
			continue
		}

		market := MarketInfo{
			ID:       m.ID,
			Question: m.Question,
		}

		// Parse window times
		if m.WindowStart != "" {
			if t, err := time.Parse(time.RFC3339, m.WindowStart); err == nil {
				market.WindowStart = t
			}
		}
		if m.WindowEnd != "" {
			if t, err := time.Parse(time.RFC3339, m.WindowEnd); err == nil {
				market.WindowEnd = t
				// Calculate status based on end time
				now := time.Now()
				if now.After(t) {
					market.Status = "ended"
				} else if t.Sub(now) < 10*time.Minute {
					market.Status = "ending_soon"
				} else {
					market.Status = "active"
				}
				// Calculate time left
				market.TimeLeft = formatDuration(t.Sub(now))
			}
		}

		for _, t := range m.Tokens {
			if t.Outcome == "Up" || t.Outcome == "YES" {
				market.UpTokenID = t.TokenID
			} else {
				market.DownTokenID = t.TokenID
			}
		}

		markets = append(markets, market)
	}

	s.mu.Lock()
	s.markets = markets
	s.mu.Unlock()
}

// generateBTC5MinMarkets generates BTC 5-minute market info based on current time
func (s *APIServer) generateBTC5MinMarkets() []MarketInfo {
	now := time.Now()

	currentMinute := now.Minute()
	windowStartMinute := (currentMinute / 5) * 5
	baseWindowStart := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), windowStartMinute, 0, 0, now.Location())

	var markets []MarketInfo

	for i := 0; i < 3; i++ {
		windowStart := baseWindowStart.Add(time.Duration(i*5) * time.Minute)
		windowEnd := windowStart.Add(5 * time.Minute)

		marketID := fmt.Sprintf("btc-updown-5m-%d", windowEnd.Unix())

		etLoc, _ := time.LoadLocation("America/New_York")
		etTime := windowStart.In(etLoc)
		question := fmt.Sprintf("Bitcoin Up or Down - %s", etTime.Format("Jan 2, 3:04PM-3:05PM MST"))

		status := "active"
		if now.After(windowEnd) {
			status = "ended"
		} else if windowEnd.Sub(now) < 10*time.Minute {
			status = "ending_soon"
		}

		market := MarketInfo{
			ID:          marketID,
			Question:    question,
			WindowStart: windowStart,
			WindowEnd:   windowEnd,
			Status:      status,
			TimeLeft:    formatDuration(windowEnd.Sub(now)),
			UpTokenID:   marketID + "-up",
			DownTokenID: marketID + "-down",
		}

		markets = append(markets, market)
	}

	return markets
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		return "Ended"
	}
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60
	if hours > 0 {
		return fmt.Sprintf("%dh%dm", hours, minutes)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm%ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstNonZeroFloat(values ...float64) float64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func buildPositionInfo(pos polymarket.Position, fallbackCurrentPrice float64) PositionInfo {
	currentPrice := firstNonZeroFloat(pos.CurPrice, fallbackCurrentPrice)
	if currentPrice == 0 && pos.Size > 0 && pos.CurrentValue > 0 {
		currentPrice = pos.CurrentValue / pos.Size
	}
	if currentPrice == 0 {
		currentPrice = pos.AvgPrice
	}

	currentValue := pos.CurrentValue
	if currentValue == 0 && pos.Size > 0 && currentPrice > 0 {
		currentValue = pos.Size * currentPrice
	}

	pnl := pos.CashPnL
	if pnl == 0 && currentValue > 0 && pos.InitialValue > 0 {
		pnl = currentValue - pos.InitialValue
	}
	if pnl == 0 && pos.Size > 0 && currentPrice > 0 && pos.AvgPrice > 0 {
		pnl = pos.Size * (currentPrice - pos.AvgPrice)
	}

	pnlPct := pos.PercentPnL
	if pnlPct == 0 && pos.InitialValue > 0 {
		pnlPct = (pnl / pos.InitialValue) * 100
	}
	if pnlPct == 0 && pos.AvgPrice > 0 && currentPrice > 0 {
		pnlPct = ((currentPrice - pos.AvgPrice) / pos.AvgPrice) * 100
	}

	marketID := firstNonEmptyString(pos.Slug, pos.ConditionId, pos.Market, pos.Asset)
	marketName := firstNonEmptyString(pos.Title, pos.Market, pos.Slug, pos.ConditionId, pos.Asset)

	return PositionInfo{
		MarketID:     marketID,
		MarketName:   marketName,
		TokenID:      pos.Asset,
		Side:         pos.Outcome,
		EntryPrice:   pos.AvgPrice,
		CurrentPrice: currentPrice,
		Size:         pos.Size,
		CurrentValue: currentValue,
		PnL:          pnl,
		PnLPct:       pnlPct,
		OpenTime:     time.Now(),
		Duration:     "Active",
		IsActive:     true,
		CloseReason:  "",
	}
}

func (s *APIServer) refreshPositions() {
	positions := make([]PositionInfo, 0)

	// Only use Polymarket API for real positions (no local data)
	if s.userAddress == "" {
		s.mu.Lock()
		s.positions = positions
		s.mu.Unlock()
		return
	}

	apiPositions, err := s.client.GetPositions(s.userAddress)
	if err != nil {
		log.Printf("[API] Failed to fetch positions from Polymarket: %v", err)
		s.mu.Lock()
		s.positions = positions
		s.mu.Unlock()
		return
	}

	for _, pos := range apiPositions {
		fallbackCurrentPrice := 0.0
		if pos.CurPrice <= 0 && (pos.CurrentValue <= 0 || pos.Size <= 0) {
			if price, err := s.client.GetPrice(pos.Asset); err == nil && price > 0 {
				fallbackCurrentPrice = price
			}
		}
		positions = append(positions, buildPositionInfo(pos, fallbackCurrentPrice))
	}

	s.mu.Lock()
	s.positions = positions
	s.mu.Unlock()
}

func (s *APIServer) refreshStrategyState() {
	// Load real stats from performance.json
	perf := s.loadPerformanceStats()

	s.mu.Lock()
	defer s.mu.Unlock()

	s.strategyState.Uptime = time.Since(s.strategyState.StartTime).Round(time.Second).String()

	if perf != nil {
		s.strategyState.TotalTrades = perf.TotalTrades
		s.strategyState.WinningTrades = perf.WinningTrades
		s.strategyState.LosingTrades = perf.LosingTrades
		s.strategyState.TotalPnL = perf.TotalPnL
		s.strategyState.Drawdown = perf.MaxDrawdown
		if perf.TotalTrades > 0 {
			s.strategyState.WinRate = perf.WinRate
		}

		// Calculate today's stats from daily_stats
		perfData, err := os.ReadFile("data/performance.json")
		if err == nil {
			var raw struct {
				DailyStats map[string]struct {
					Trades int     `json:"trades"`
					PnL    float64 `json:"pnl"`
					Wins   int     `json:"wins"`
					Losses int     `json:"losses"`
				} `json:"daily_stats"`
			}
			if json.Unmarshal(perfData, &raw) == nil {
				today := time.Now().Format("2006-01-02")
				if ds, ok := raw.DailyStats[today]; ok {
					s.strategyState.DailyPnL = ds.PnL
					s.strategyState.DailyTrades = ds.Trades
				}

				// Calculate consecutive wins/losses from trade_results
				var trRaw struct {
					TradeResults []struct {
						Success bool `json:"success"`
					} `json:"trade_results"`
				}
				if json.Unmarshal(perfData, &trRaw) == nil && len(trRaw.TradeResults) > 0 {
					results := trRaw.TradeResults
					lastResult := results[len(results)-1].Success
					streak := 0
					for i := len(results) - 1; i >= 0; i-- {
						if results[i].Success == lastResult {
							streak++
						} else {
							break
						}
					}
					if lastResult {
						s.strategyState.ConsecutiveWins = streak
						s.strategyState.ConsecutiveLoss = 0
					} else {
						s.strategyState.ConsecutiveWins = 0
						s.strategyState.ConsecutiveLoss = streak
					}
				}
			}
		}
	}
}

func (s *APIServer) getCurrentIndicators() TechnicalIndicators {
	prices := s.spotMonitor.GetPriceHistory()
	currentPrice := s.spotMonitor.GetCurrentPrice()
	trend, strength := s.spotMonitor.GetTrend(30)

	var rsi, macdHist, bollingerUp, bollingerMid, bollingerLow, bollingerPct, atr, momentum float64

	if len(prices) >= 20 {
		priceArr := make([]float64, len(prices))
		for i, p := range prices {
			priceArr[i] = p.Price
		}

		indicators := btc.NewTechnicalIndicators()
		rsi = indicators.RSI(priceArr, 14)
		macd := indicators.MACD(priceArr)
		macdHist = macd.Histogram
		bollinger := indicators.Bollinger(priceArr, 20, 2.0)
		bollingerUp = bollinger.Upper
		bollingerMid = bollinger.Middle
		bollingerLow = bollinger.Lower
		bollingerPct = bollinger.PercentB
		atr = indicators.ATRFromPrices(priceArr, 14)
		momentum = indicators.Momentum(priceArr, 10)
	}

	return TechnicalIndicators{
		SpotPrice:     currentPrice,
		RSI:           rsi,
		MACDHist:      macdHist,
		BollingerUp:   bollingerUp,
		BollingerMid:  bollingerMid,
		BollingerLow:  bollingerLow,
		BollingerPct:  bollingerPct,
		ATR:           atr,
		Momentum:      momentum,
		Trend:         trend,
		TrendStrength: strength,
		Timestamp:     time.Now(),
	}
}

func (s *APIServer) getRecentTrades(limit int) []TradeInfo {
	if len(s.trades) <= limit {
		return s.trades
	}
	return s.trades[:limit]
}

func (s *APIServer) getRecentAlerts(limit int) []AlertInfo {
	if len(s.alerts) <= limit {
		return s.alerts
	}
	return s.alerts[len(s.alerts)-limit:]
}

func (s *APIServer) getPriceHistory(limit int) []PricePoint {
	history := s.spotMonitor.GetPriceHistory()
	if len(history) <= limit {
		result := make([]PricePoint, len(history))
		for i, p := range history {
			result[i] = PricePoint{Price: p.Price, Timestamp: p.Timestamp}
		}
		return result
	}
	start := len(history) - limit
	result := make([]PricePoint, limit)
	for i := start; i < len(history); i++ {
		result[i-start] = PricePoint{Price: history[i].Price, Timestamp: history[i].Timestamp}
	}
	return result
}

func (s *APIServer) getDashboardData() DashboardData {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return DashboardData{
		Strategy:     s.strategyState,
		Indicators:   s.getCurrentIndicators(),
		Markets:      s.markets,
		Positions:    s.positions,
		RecentTrades: s.getRecentTrades(20),
		Alerts:       s.getRecentAlerts(10),
		PriceHistory: s.getPriceHistory(100),
		Performance:  s.loadPerformanceStats(),
	}
}

// handlePerformance returns performance statistics
func (s *APIServer) handlePerformance(w http.ResponseWriter, r *http.Request) {
	stats := s.loadPerformanceStats()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// loadPerformanceStats loads performance data from file
func (s *APIServer) loadPerformanceStats() *btc.PerformanceStats {
	data, err := os.ReadFile("data/performance.json")
	if err != nil {
		return nil
	}

	var saved struct {
		TotalTrades    int                              `json:"total_trades"`
		WinningTrades  int                              `json:"winning_trades"`
		LosingTrades   int                              `json:"losing_trades"`
		TotalPnL       float64                          `json:"total_pnl"`
		BestTrade      float64                          `json:"best_trade"`
		WorstTrade     float64                          `json:"worst_trade"`
		ClaimAttempts  int                              `json:"claim_attempts"`
		ClaimSuccesses int                              `json:"claim_successes"`
		MaxDrawdown    float64                          `json:"max_drawdown"`
		PeakBalance    float64                          `json:"peak_balance"`
		StartTime      time.Time                        `json:"start_time"`
		LastUpdated    time.Time                        `json:"last_updated"`
		DailyStats     map[string]*btc.DailyPerformance `json:"daily_stats"`
		TradeResults   []btc.TradeResult                `json:"trade_results"`
	}

	if err := json.Unmarshal(data, &saved); err != nil {
		return nil
	}

	// Convert max drawdown from absolute USD to percentage of peak
	drawdownPct := saved.MaxDrawdown
	if saved.PeakBalance > 0 {
		drawdownPct = (saved.MaxDrawdown / saved.PeakBalance) * 100
	}

	stats := &btc.PerformanceStats{
		TotalTrades:    saved.TotalTrades,
		WinningTrades:  saved.WinningTrades,
		LosingTrades:   saved.LosingTrades,
		TotalPnL:       saved.TotalPnL,
		BestTrade:      saved.BestTrade,
		WorstTrade:     saved.WorstTrade,
		ClaimAttempts:  saved.ClaimAttempts,
		ClaimSuccesses: saved.ClaimSuccesses,
		MaxDrawdown:    drawdownPct,
		StartTime:      saved.StartTime.Format(time.RFC3339),
		LastUpdated:    saved.LastUpdated.Format(time.RFC3339),
		Uptime:         time.Since(saved.StartTime).Round(time.Second).String(),
	}

	if saved.TotalTrades > 0 {
		stats.WinRate = float64(saved.WinningTrades) / float64(saved.TotalTrades) * 100
		stats.AveragePnL = saved.TotalPnL / float64(saved.TotalTrades)
	}

	if saved.ClaimAttempts > 0 {
		stats.ClaimSuccessRate = float64(saved.ClaimSuccesses) / float64(saved.ClaimAttempts) * 100
	}

	// Calculate Sharpe ratio from daily stats
	if len(saved.DailyStats) >= 2 {
		dailyPnLs := make([]float64, 0, len(saved.DailyStats))
		for _, d := range saved.DailyStats {
			dailyPnLs = append(dailyPnLs, d.PnL)
		}
		var pnlSum float64
		for _, p := range dailyPnLs {
			pnlSum += p
		}
		meanPnL := pnlSum / float64(len(dailyPnLs))
		var variance float64
		for _, p := range dailyPnLs {
			diff := p - meanPnL
			variance += diff * diff
		}
		stdDev := math.Sqrt(variance / float64(len(dailyPnLs)-1))
		if stdDev > 0 {
			stats.SharpeRatio = (meanPnL / stdDev) * math.Sqrt(250)
		}
	}

	return stats
}

// loadTradeResults loads trade results from performance.json for PnL enrichment
func (s *APIServer) loadTradeResults() []btc.TradeResult {
	data, err := os.ReadFile("data/performance.json")
	if err != nil {
		return nil
	}

	var saved struct {
		TradeResults []btc.TradeResult `json:"trade_results"`
	}

	if err := json.Unmarshal(data, &saved); err != nil {
		return nil
	}

	return saved.TradeResults
}

// loadPositions loads position data from positions.json for entry price lookups
func (s *APIServer) loadPositions() map[string]*btc.Position {
	data, err := os.ReadFile("data/positions.json")
	if err != nil {
		return nil
	}
	var positions map[string]*btc.Position
	if err := json.Unmarshal(data, &positions); err != nil {
		return nil
	}
	return positions
}

// handleNotifications returns recent notification events
func (s *APIServer) handleNotifications(w http.ResponseWriter, r *http.Request) {
	events := s.loadNotificationEvents()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(events)
}

// loadNotificationEvents loads notification events from file
func (s *APIServer) loadNotificationEvents() []notification.Event {
	data, err := os.ReadFile("data/notifications.json")
	if err != nil {
		return []notification.Event{}
	}

	var events []notification.Event
	if err := json.Unmarshal(data, &events); err != nil {
		return []notification.Event{}
	}

	return events
}

// handleNotificationsConfig handles notification configuration
func (s *APIServer) handleNotificationsConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		config := s.loadNotificationConfig()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(config)
	case "POST":
		var config notification.Config
		if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := s.saveNotificationConfig(config); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// loadNotificationConfig loads notification configuration from file
func (s *APIServer) loadNotificationConfig() notification.Config {
	data, err := os.ReadFile("data/notification_config.json")
	if err != nil {
		return notification.DefaultConfig()
	}

	var config notification.Config
	if err := json.Unmarshal(data, &config); err != nil {
		return notification.DefaultConfig()
	}

	return config
}

// saveNotificationConfig saves notification configuration to file
func (s *APIServer) saveNotificationConfig(config notification.Config) error {
	jsonData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile("data/notification_config.json", jsonData, 0644)
}

func (s *APIServer) handleCosts(w http.ResponseWriter, r *http.Request) {
	stats := s.loadCostStats()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

func (s *APIServer) loadCostStats() cost.Stats {
	data, err := os.ReadFile("data/costs.json")
	if err != nil {
		return cost.Stats{ByProvider: make(map[cost.Provider]cost.ProviderStats)}
	}

	var saved struct {
		Records    []cost.UsageRecord          `json:"records"`
		DailyUsage map[string]*cost.DailyUsage `json:"daily_usage"`
	}

	if err := json.Unmarshal(data, &saved); err != nil {
		return cost.Stats{ByProvider: make(map[cost.Provider]cost.ProviderStats)}
	}

	stats := cost.Stats{
		ByProvider: make(map[cost.Provider]cost.ProviderStats),
	}

	now := time.Now()
	today := now.Format("2006-01-02")
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())

	providerStats := make(map[cost.Provider]*cost.ProviderStats)

	for _, r := range saved.Records {
		stats.TotalCost += r.Cost
		stats.TotalRequests++

		if r.Timestamp.Format("2006-01-02") == today {
			stats.TodayCost += r.Cost
			stats.TodayRequests++
		}

		if r.Timestamp.After(monthStart) {
			stats.MonthlyCost += r.Cost
			stats.MonthlyRequests++
		}

		if _, exists := providerStats[r.Provider]; !exists {
			providerStats[r.Provider] = &cost.ProviderStats{}
		}
		ps := providerStats[r.Provider]
		ps.TotalCost += r.Cost
		ps.RequestCount++
	}

	for p, ps := range providerStats {
		avg := 0.0
		if ps.RequestCount > 0 {
			avg = ps.TotalCost / float64(ps.RequestCount)
		}
		stats.ByProvider[p] = cost.ProviderStats{
			TotalCost:     ps.TotalCost,
			RequestCount:  ps.RequestCount,
			AvgCostPerReq: avg,
		}
	}

	return stats
}

func (s *APIServer) handleCostsDaily(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile("data/costs.json")
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]cost.DailyUsage{})
		return
	}

	var saved struct {
		DailyUsage map[string]*cost.DailyUsage `json:"daily_usage"`
	}

	if err := json.Unmarshal(data, &saved); err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]cost.DailyUsage{})
		return
	}

	result := make([]cost.DailyUsage, 0)
	now := time.Now()

	for i := 0; i < 7; i++ {
		date := now.AddDate(0, 0, -i).Format("2006-01-02")
		if daily, exists := saved.DailyUsage[date]; exists {
			result = append(result, *daily)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// AccountInfo returned by /api/account
type AccountInfo struct {
	WalletAddress  string        `json:"wallet_address"`
	USDCBalance    float64       `json:"usdc_balance"`
	PositionsValue float64       `json:"positions_value"`
	PortfolioValue float64       `json:"portfolio_value"`
	DailyStats     []DailyStat   `json:"daily_stats"`
	EquityCurve    []EquityPoint `json:"equity_curve"`
}

type DailyStat struct {
	Date    string  `json:"date"`
	Trades  int     `json:"trades"`
	Wins    int     `json:"wins"`
	Losses  int     `json:"losses"`
	PnL     float64 `json:"pnl"`
	WinRate float64 `json:"win_rate"`
}

type EquityPoint struct {
	Date  string  `json:"date"`
	Value float64 `json:"value"`
}

type accountDailyStatSnapshot struct {
	Date    string  `json:"date"`
	Trades  int     `json:"trades"`
	Wins    int     `json:"wins"`
	Losses  int     `json:"losses"`
	PnL     float64 `json:"pnl"`
	WinRate float64 `json:"win_rate"`
}

type accountPerformanceSnapshot struct {
	TotalPnL   float64                             `json:"total_pnl"`
	DailyStats map[string]accountDailyStatSnapshot `json:"daily_stats"`
}

func positionMarketValue(pos PositionInfo) float64 {
	if pos.CurrentValue > 0 {
		return pos.CurrentValue
	}
	if pos.Size > 0 && pos.CurrentPrice > 0 {
		return pos.Size * pos.CurrentPrice
	}
	return 0
}

func buildAccountInfo(address string, usdcBalance float64, positions []PositionInfo, perf accountPerformanceSnapshot) AccountInfo {
	positionsValue := 0.0
	for _, pos := range positions {
		if pos.IsActive {
			positionsValue += positionMarketValue(pos)
		}
	}

	dailyStats := make([]DailyStat, 0, len(perf.DailyStats))
	equityCurve := make([]EquityPoint, 0, len(perf.DailyStats))
	dates := make([]string, 0, len(perf.DailyStats))
	for date := range perf.DailyStats {
		dates = append(dates, date)
	}
	sort.Strings(dates)

	portfolioValue := usdcBalance + positionsValue
	startingCapital := portfolioValue - perf.TotalPnL
	if startingCapital < 0 {
		startingCapital = 0
	}

	cumulativePnL := 0.0
	for _, date := range dates {
		stat := perf.DailyStats[date]
		statDate := firstNonEmptyString(stat.Date, date)
		dailyStats = append(dailyStats, DailyStat{
			Date:    statDate,
			Trades:  stat.Trades,
			Wins:    stat.Wins,
			Losses:  stat.Losses,
			PnL:     stat.PnL,
			WinRate: stat.WinRate,
		})

		cumulativePnL += stat.PnL
		equityCurve = append(equityCurve, EquityPoint{
			Date:  statDate,
			Value: startingCapital + cumulativePnL,
		})
	}

	return AccountInfo{
		WalletAddress:  address,
		USDCBalance:    usdcBalance,
		PositionsValue: positionsValue,
		PortfolioValue: portfolioValue,
		DailyStats:     dailyStats,
		EquityCurve:    equityCurve,
	}
}

// queryUSDCBalance queries USDC.e balance from Polygon RPC
func queryUSDCBalance(walletAddress string) (float64, error) {
	usdceContract := "0x2791Bca1f2de4661ED88A30C99A7a9449Aa84174"
	// balanceOf(address) = 0x70a08231 + address padded to 32 bytes
	addr := strings.TrimPrefix(strings.ToLower(walletAddress), "0x")
	data := "0x70a08231" + strings.Repeat("0", 24) + addr

	rpcURL := os.Getenv("POLYGON_RPC_URL")
	if rpcURL == "" {
		rpcURL = "https://polygon-bor-rpc.publicnode.com"
	}

	body := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "eth_call",
		"params": []interface{}{
			map[string]string{
				"to":   usdceContract,
				"data": data,
			},
			"latest",
		},
		"id": 1,
	}

	jsonBody, _ := json.Marshal(body)
	resp, err := http.Post(rpcURL, "application/json", bytes.NewReader(jsonBody))
	if err != nil {
		return 0, fmt.Errorf("RPC call failed: %w", err)
	}
	defer resp.Body.Close()

	var rpcResp struct {
		Result string `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return 0, fmt.Errorf("decode RPC response: %w", err)
	}
	if rpcResp.Error != nil {
		return 0, fmt.Errorf("RPC error: %s", rpcResp.Error.Message)
	}

	// Parse hex result → uint256 → float64 with 6 decimals (USDC.e)
	hexStr := strings.TrimPrefix(rpcResp.Result, "0x")
	if hexStr == "" || hexStr == "0" {
		return 0, nil
	}
	rawBytes, err := hex.DecodeString(strings.Repeat("0", 64-len(hexStr)) + hexStr)
	if err != nil {
		return 0, fmt.Errorf("decode hex: %w", err)
	}
	balance := new(big.Int).SetBytes(rawBytes)
	// USDC.e has 6 decimals
	balFloat := new(big.Float).SetInt(balance)
	divisor := new(big.Float).SetFloat64(math.Pow10(6))
	result := new(big.Float).Quo(balFloat, divisor)
	f, _ := result.Float64()
	return f, nil
}

func (s *APIServer) handleAccount(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	address := s.userAddress
	if address == "" {
		json.NewEncoder(w).Encode(AccountInfo{WalletAddress: "unknown"})
		return
	}

	s.mu.RLock()
	positions := append([]PositionInfo(nil), s.positions...)
	s.mu.RUnlock()

	var usdcBalance float64
	if onChainBalance, err := queryUSDCBalance(address); err == nil {
		usdcBalance = onChainBalance
	} else {
		log.Printf("[API] Failed to query on-chain USDC balance for %s: %v", address, err)
	}

	var perf accountPerformanceSnapshot
	perfFile := "data/performance.json"
	if raw, err := os.ReadFile(perfFile); err == nil {
		if err := json.Unmarshal(raw, &perf); err != nil {
			log.Printf("[API] Failed to parse performance snapshot: %v", err)
		}
	}

	json.NewEncoder(w).Encode(buildAccountInfo(address, usdcBalance, positions, perf))
}

func main() {
	server := NewAPIServer()
	if err := server.Start(); err != nil {
		log.Fatalf("Failed to start API server: %v", err)
	}
}
