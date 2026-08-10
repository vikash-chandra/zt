package scanner

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type RSSChannel struct {
	Items []RSSItem `xml:"channel>item"`
}

type RSSItem struct {
	Title   string `xml:"title"`
	Link    string `xml:"link"`
	PubDate string `xml:"pubDate"`
}

// NewsAggregator fetches free financial news headlines from Yahoo Finance RSS & Google News
type NewsAggregator struct {
	client *http.Client
}

func NewNewsAggregator() *NewsAggregator {
	return &NewsAggregator{
		client: &http.Client{
			Timeout: 1 * time.Second,
		},
	}
}

// FetchNewsForStock retrieves news headlines and computes sentiment for a given stock symbol
func (n *NewsAggregator) FetchNewsForStock(symbol string) ([]NewsItem, string, string) {
	var items []NewsItem
	var err error

	searchQuery := symbol + "+stock+India"
	if symbol == "GOLD" {
		searchQuery = "Gold+price+India+MCX"
	} else if symbol == "CRUDEOIL" {
		searchQuery = "Crude+Oil+price+India+MCX"
	} else if symbol == "NIFTY 50" {
		searchQuery = "Nifty+50+index+India"
	}

	// Try Yahoo Finance RSS first for regular equities
	if symbol != "GOLD" && symbol != "CRUDEOIL" && symbol != "NIFTY 50" {
		yahooURL := fmt.Sprintf("https://finance.yahoo.com/rss/headline?s=%s.NS", symbol)
		items, err = n.fetchRSS(yahooURL, "Yahoo Finance")
	}

	// Fallback or query Google News RSS directly for commodities & indices
	if err != nil || len(items) == 0 {
		googleURL := fmt.Sprintf("https://news.google.com/rss/search?q=%s&hl=en-IN&gl=IN&ceid=IN:en", searchQuery)
		gItems, gErr := n.fetchRSS(googleURL, "Google News")
		if gErr == nil && len(gItems) > 0 {
			items = append(items, gItems...)
		}
	}

	if len(items) > 5 {
		items = items[:5]
	}

	if len(items) == 0 {
		return nil, "No recent news found", "NEUTRAL"
	}

	var headlines []string
	posCount, negCount := 0, 0

	for i := range items {
		sentiment := analyzeSentiment(items[i].Title)
		items[i].Sentiment = sentiment

		if sentiment == "POSITIVE" {
			posCount++
		} else if sentiment == "NEGATIVE" {
			negCount++
		}
		headlines = append(headlines, items[i].Title)
	}

	overallSentiment := "NEUTRAL"
	if posCount > negCount {
		overallSentiment = "POSITIVE"
	} else if negCount > posCount {
		overallSentiment = "NEGATIVE"
	}

	summary := strings.Join(headlines, " | ")
	return items, summary, overallSentiment
}

func (n *NewsAggregator) fetchRSS(url string, source string) ([]NewsItem, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := n.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP error %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var channel RSSChannel
	if err := xml.Unmarshal(body, &channel); err != nil {
		return nil, err
	}

	var news []NewsItem
	for _, item := range channel.Items {
		pubTime, _ := time.Parse(time.RFC1123, item.PubDate)
		if pubTime.IsZero() {
			pubTime = time.Now()
		}
		news = append(news, NewsItem{
			Title:     item.Title,
			Link:      item.Link,
			Source:    source,
			Published: pubTime,
		})
	}

	return news, nil
}

// analyzeSentiment performs keyword-based financial sentiment classification
func analyzeSentiment(text string) string {
	lower := strings.ToLower(text)

	bullishKeywords := []string{
		"profit", "surge", "gain", "upgrade", "order", "rally", "growth",
		"record", "expansion", "bullish", "jump", "high", "buy", "outperform",
		"revenue", "dividend", "acquisition", "win", "approval",
	}

	bearishKeywords := []string{
		"loss", "fall", "drop", "downgrade", "penalty", "slump", "decline",
		"bearish", "plunge", "low", "sell", "underperform", "investigation",
		"strike", "fraud", "warning", "cut", "default",
	}

	posScore, negScore := 0, 0
	for _, kw := range bullishKeywords {
		if strings.Contains(lower, kw) {
			posScore++
		}
	}
	for _, kw := range bearishKeywords {
		if strings.Contains(lower, kw) {
			negScore++
		}
	}

	if posScore > negScore {
		return "POSITIVE"
	}
	if negScore > posScore {
		return "NEGATIVE"
	}
	return "NEUTRAL"
}
