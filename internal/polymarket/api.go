package polymarket

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"
)

const (
	GammaAPIURL = "https://gamma-api.polymarket.com"
	ClobAPIURL  = "https://clob.polymarket.com"
	DataAPIURL  = "https://data-api.polymarket.com"
)

type Client struct {
	httpClient *http.Client
}

func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (c *Client) doGET(url string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "poly-bot/1.0")
	return c.httpClient.Do(req)
}

// GammaMarket represents a market from the Gamma API
type GammaMarket struct {
	ID            string `json:"id"`
	Question      string `json:"question"`
	Active        bool   `json:"active"`
	Closed        bool   `json:"closed"`
	ClobTokenIds  string `json:"clobTokenIds"`  // JSON string of array
	Outcomes      string `json:"outcomes"`      // JSON string of array
	OutcomePrices string `json:"outcomePrices"` // JSON string of array
	StartDate     string `json:"startDate"`
	EndDate       string `json:"endDate"`
}

// ParsedMarket is our internal representation after parsing JSON strings
type ParsedMarket struct {
	ID          string
	Question    string
	Tokens      []TokenInfo
	WindowStart string
	WindowEnd   string
}

type TokenInfo struct {
	TokenID string
	Outcome string
	Price   float64
}

// GetMarkets fetches a list of markets and parses the stringified arrays.
// Orders by volume to get the most liquid/active markets.
func (c *Client) GetMarkets(limit int) ([]ParsedMarket, error) {
	// Query: active=true, closed=false, order by volume (most liquid markets)
	url := fmt.Sprintf("%s/markets?limit=%d&active=true&closed=false", GammaAPIURL, limit)

	resp, err := c.doGET(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var rawMarkets []GammaMarket
	if err := json.NewDecoder(resp.Body).Decode(&rawMarkets); err != nil {
		return nil, err
	}

	var parsedMarkets []ParsedMarket
	for _, m := range rawMarkets {
		var tokenIds, outcomes, prices []string

		if err := json.Unmarshal([]byte(m.ClobTokenIds), &tokenIds); err != nil {
			log.Printf("[API] Warning: Failed to parse clobTokenIds for market %s: %v", m.ID, err)
		}
		if err := json.Unmarshal([]byte(m.Outcomes), &outcomes); err != nil {
			log.Printf("[API] Warning: Failed to parse outcomes for market %s: %v", m.ID, err)
		}
		if err := json.Unmarshal([]byte(m.OutcomePrices), &prices); err != nil {
			log.Printf("[API] Warning: Failed to parse outcomePrices for market %s: %v", m.ID, err)
		}

		// Make sure arrays are aligned
		if len(tokenIds) == 0 || len(tokenIds) != len(outcomes) || len(tokenIds) != len(prices) {
			continue
		}

		pm := ParsedMarket{
			ID:          m.ID,
			Question:    m.Question,
			Tokens:      make([]TokenInfo, len(tokenIds)),
			WindowStart: m.StartDate,
			WindowEnd:   m.EndDate,
		}

		for i := range tokenIds {
			price, _ := strconv.ParseFloat(prices[i], 64)
			pm.Tokens[i] = TokenInfo{
				TokenID: tokenIds[i],
				Outcome: outcomes[i],
				Price:   price,
			}
		}

		parsedMarkets = append(parsedMarkets, pm)
	}

	return parsedMarkets, nil
}

// Orderbook represents the L2 orderbook from CLOB API
type Orderbook struct {
	Bids []Order `json:"bids"`
	Asks []Order `json:"asks"`
}

type Order struct {
	Price string `json:"price"`
	Size  string `json:"size"`
}

// GetOrderbook fetches the orderbook for a specific token
func (c *Client) GetOrderbook(tokenID string) (*Orderbook, error) {
	url := fmt.Sprintf("%s/book?token_id=%s", ClobAPIURL, tokenID)

	resp, err := c.doGET(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("clob API status code: %d", resp.StatusCode)
	}

	var ob Orderbook
	if err := json.NewDecoder(resp.Body).Decode(&ob); err != nil {
		return nil, err
	}

	return &ob, nil
}

// GetCLOBPrice fetches the computed price for a token from the CLOB matching engine.
// This includes complementary matching and returns the actual executable price,
// even when the visible orderbook appears to have only extreme prices.
func (c *Client) GetCLOBPrice(tokenID string, side string) (float64, error) {
	url := fmt.Sprintf("%s/price?token_id=%s&side=%s", ClobAPIURL, tokenID, side)

	resp, err := c.doGET(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("clob price API status code: %d", resp.StatusCode)
	}

	var result struct {
		Price string `json:"price"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}

	price, err := strconv.ParseFloat(result.Price, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse CLOB price: %v", err)
	}

	return price, nil
}

// GetEventBySlug fetches an event by its slug (e.g., "btc-updown-5m-1773288900")
func (c *Client) GetEventBySlug(slug string) (*GammaEvent, error) {
	url := fmt.Sprintf("%s/events?slug=%s", GammaAPIURL, slug)

	resp, err := c.doGET(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var events []GammaEvent
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		return nil, err
	}

	if len(events) == 0 {
		return nil, fmt.Errorf("event not found: %s", slug)
	}

	return &events[0], nil
}

// GammaEvent represents an event from the Gamma API (contains markets)
type GammaEvent struct {
	ID      string        `json:"id"`
	Slug    string        `json:"slug"`
	Title   string        `json:"title"`
	Active  bool          `json:"active"`
	Closed  bool          `json:"closed"`
	Markets []GammaMarket `json:"markets"`
}

// GetBTCMarket fetches a BTC Up/Down market for a specific timestamp
// Returns the market with token IDs for Up and Down outcomes
func (c *Client) GetBTCMarket(timestamp int64) (*ParsedMarket, error) {
	slug := fmt.Sprintf("btc-updown-5m-%d", timestamp)

	event, err := c.GetEventBySlug(slug)
	if err != nil {
		return nil, err
	}

	if len(event.Markets) == 0 {
		return nil, fmt.Errorf("no markets found in event: %s", slug)
	}

	// Parse the first market (usually there's only one per event)
	m := event.Markets[0]

	var tokenIds, outcomes, prices []string
	if err := json.Unmarshal([]byte(m.ClobTokenIds), &tokenIds); err != nil {
		log.Printf("[API] Warning: Failed to parse clobTokenIds for market %s: %v", m.ID, err)
	}
	if err := json.Unmarshal([]byte(m.Outcomes), &outcomes); err != nil {
		log.Printf("[API] Warning: Failed to parse outcomes for market %s: %v", m.ID, err)
	}
	if err := json.Unmarshal([]byte(m.OutcomePrices), &prices); err != nil {
		log.Printf("[API] Warning: Failed to parse outcomePrices for market %s: %v", m.ID, err)
	}

	if len(tokenIds) < 2 {
		return nil, fmt.Errorf("insufficient token IDs in market: %s", slug)
	}

	pm := &ParsedMarket{
		ID:       m.ID,
		Question: m.Question,
		Tokens:   make([]TokenInfo, len(tokenIds)),
	}

	for i := range tokenIds {
		price, _ := strconv.ParseFloat(prices[i], 64)
		pm.Tokens[i] = TokenInfo{
			TokenID: tokenIds[i],
			Outcome: outcomes[i],
			Price:   price,
		}
	}

	return pm, nil
}

// GetMarketPrices fetches current prices for a market by slug
// Returns (upPrice, downPrice, error)
func (c *Client) GetMarketPrices(slug string) (float64, float64, error) {
	event, err := c.GetEventBySlug(slug)
	if err != nil {
		return 0, 0, err
	}

	if len(event.Markets) == 0 {
		return 0, 0, fmt.Errorf("no markets found in event: %s", slug)
	}

	m := event.Markets[0]
	var outcomes, prices []string
	_ = json.Unmarshal([]byte(m.Outcomes), &outcomes)
	_ = json.Unmarshal([]byte(m.OutcomePrices), &prices)

	if len(prices) < 2 {
		return 0, 0, fmt.Errorf("insufficient prices in market: %s", slug)
	}

	var upPrice, downPrice float64
	for i, outcome := range outcomes {
		price, _ := strconv.ParseFloat(prices[i], 64)
		if outcome == "Up" {
			upPrice = price
		} else if outcome == "Down" {
			downPrice = price
		}
	}

	return upPrice, downPrice, nil
}

// MarketSpread holds effective best bid/ask/spread from Gamma API
// These prices are for the FIRST outcome (typically "Up") and reflect complementary matching
type MarketSpread struct {
	BestBid float64
	BestAsk float64
	Spread  float64
}

// GetMarketSpread fetches the effective best bid/ask from the Gamma API /markets endpoint
// Returns prices for the first outcome (UP). For DOWN: bestAsk = 1 - spread.BestBid
func (c *Client) GetMarketSpread(slug string) (*MarketSpread, error) {
	url := fmt.Sprintf("%s/markets?slug=%s", GammaAPIURL, slug)

	resp, err := c.doGET(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gamma API status code: %d", resp.StatusCode)
	}

	var markets []struct {
		BestBid float64 `json:"bestBid"`
		BestAsk float64 `json:"bestAsk"`
		Spread  float64 `json:"spread"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&markets); err != nil {
		return nil, err
	}

	if len(markets) == 0 {
		return nil, fmt.Errorf("market not found: %s", slug)
	}

	m := markets[0]
	bid := m.BestBid
	ask := m.BestAsk
	spread := m.Spread

	if bid <= 0 && ask <= 0 {
		return nil, fmt.Errorf("no bid/ask data for market: %s", slug)
	}

	return &MarketSpread{BestBid: bid, BestAsk: ask, Spread: spread}, nil
}

// Trade represents a trade from the Data API
type Trade struct {
	ProxyWallet     string  `json:"proxyWallet"`
	Side            string  `json:"side"`
	Asset           string  `json:"asset"`
	ConditionId     string  `json:"conditionId"`
	Size            float64 `json:"size"`
	Price           float64 `json:"price"`
	Timestamp       int64   `json:"timestamp"`
	Title           string  `json:"title"`
	Slug            string  `json:"slug"`
	Icon            string  `json:"icon"`
	EventSlug       string  `json:"eventSlug"`
	Outcome         string  `json:"outcome"`
	OutcomeIndex    int     `json:"outcomeIndex"`
	Name            string  `json:"name"`
	Pseudonym       string  `json:"pseudonym"`
	TransactionHash string  `json:"transactionHash"`
}

// Position represents a position from the Data API
type Position struct {
	Asset              string  `json:"asset"`
	Size               float64 `json:"size"`
	AvgPrice           float64 `json:"avgPrice"`
	InitialValue       float64 `json:"initialValue"`
	CurrentValue       float64 `json:"currentValue"`
	CashPnL            float64 `json:"cashPnl"`
	PercentPnL         float64 `json:"percentPnl"`
	TotalBought        float64 `json:"totalBought"`
	RealizedPnL        float64 `json:"realizedPnl"`
	PercentRealizedPnL float64 `json:"percentRealizedPnl"`
	CurPrice           float64 `json:"curPrice"`
	Market             string  `json:"market"`
	ConditionId        string  `json:"conditionId"`
	Title              string  `json:"title"`
	Slug               string  `json:"slug"`
	EventSlug          string  `json:"eventSlug"`
	Icon               string  `json:"icon"`
	Outcome            string  `json:"outcome"`
	OutcomeIndex       int     `json:"outcomeIndex"`
	OppositeOutcome    string  `json:"oppositeOutcome"`
	OppositeAsset      string  `json:"oppositeAsset"`
	EndDate            string  `json:"endDate"`
	Redeemable         bool    `json:"redeemable"`
	Mergeable          bool    `json:"mergeable"`
	NegativeRisk       bool    `json:"negativeRisk"`
}

// GetTrades fetches trades for a user from the Data API
// This is a public API that doesn't require authentication
func (c *Client) GetTrades(userAddress string, limit int) ([]Trade, error) {
	if limit <= 0 {
		limit = 100
	}
	url := fmt.Sprintf("%s/trades?user=%s&limit=%d", DataAPIURL, userAddress, limit)

	resp, err := c.doGET(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("data API status code: %d", resp.StatusCode)
	}

	var trades []Trade
	if err := json.NewDecoder(resp.Body).Decode(&trades); err != nil {
		return nil, err
	}

	return trades, nil
}

// GetPositions fetches current positions for a user from the Data API
// This is a public API that doesn't require authentication
func (c *Client) GetPositions(userAddress string) ([]Position, error) {
	url := fmt.Sprintf("%s/positions?user=%s", DataAPIURL, userAddress)

	resp, err := c.doGET(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("data API status code: %d", resp.StatusCode)
	}

	var positions []Position
	if err := json.NewDecoder(resp.Body).Decode(&positions); err != nil {
		return nil, err
	}

	return positions, nil
}

// GetPrice fetches the current price for a token from the CLOB API
func (c *Client) GetPrice(tokenID string) (float64, error) {
	url := fmt.Sprintf("%s/price?token_id=%s", ClobAPIURL, tokenID)

	resp, err := c.doGET(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("clob API status code: %d", resp.StatusCode)
	}

	var result struct {
		Price string `json:"price"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}

	return strconv.ParseFloat(result.Price, 64)
}
