package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type LogLine struct {
	Level    string `json:"level"`
	Ts       string `json:"ts"`
	Caller   string `json:"caller"`
	Msg      string `json:"msg"`
	Error    string `json:"error"`
	Err      string `json:"err"`
	Symbol   string `json:"symbol"`
	Strategy string `json:"strategy"`
	Count    int    `json:"count"`
	Reason   string `json:"reason"`
}

func main() {
	file, err := os.Open("todays_bot_logs.txt")
	if err != nil {
		fmt.Printf("Failed to open log file: %v\n", err)
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)

	// Counters
	levelCounts := make(map[string]int)
	uniqueErrors := make(map[string]int)
	uniqueWarnings := make(map[string]int)

	// Events tracking
	var startupLines []string
	var watchlistLines []string
	var biasLines []string
	var signalLines []string
	var orderLines []string
	var connectionLines []string

	lineCount := 0

	for scanner.Scan() {
		lineCount++
		text := scanner.Text()
		
		// Clean docker line prefix "app-1  | "
		if idx := strings.Index(text, " | "); idx != -1 {
			text = text[idx+3:]
		} else if strings.HasPrefix(text, "app-1  | ") {
			text = strings.TrimPrefix(text, "app-1  | ")
		}

		text = strings.TrimSpace(text)
		if text == "" || !strings.HasPrefix(text, "{") {
			continue
		}

		var l LogLine
		if err := json.Unmarshal([]byte(text), &l); err != nil {
			// fallback if it's not valid JSON
			continue
		}

		// Update level counts
		level := strings.ToLower(l.Level)
		levelCounts[level]++

		// Track specific message patterns
		msg := l.Msg
		lowerMsg := strings.ToLower(msg)

		// Collect errors & warnings
		if level == "error" || level == "critical" {
			errStr := l.Error
			if errStr == "" {
				errStr = l.Err
			}
			key := fmt.Sprintf("%s (caller: %s)", msg, l.Caller)
			if errStr != "" {
				key = fmt.Sprintf("%s - Err: %s (caller: %s)", msg, errStr, l.Caller)
			}
			uniqueErrors[key]++
		} else if level == "warn" {
			key := fmt.Sprintf("%s (caller: %s)", msg, l.Caller)
			uniqueWarnings[key]++
		}

		// Track startup
		if strings.Contains(lowerMsg, "started") || strings.Contains(lowerMsg, "initialized") || strings.Contains(lowerMsg, "startup checks") {
			startupLines = append(startupLines, fmt.Sprintf("[%s] %s", l.Ts, msg))
		}

		// Track watchlist selection
		if strings.Contains(lowerMsg, "watchlist") || strings.Contains(lowerMsg, "selector") || strings.Contains(lowerMsg, "constituent") {
			var details string
			if l.Symbol != "" {
				details = fmt.Sprintf(" (Symbol: %s, Strategy: %s)", l.Symbol, l.Strategy)
			}
			watchlistLines = append(watchlistLines, fmt.Sprintf("[%s] %s%s", l.Ts, msg, details))
		}

		// Track bias
		if strings.Contains(lowerMsg, "bias") {
			biasLines = append(biasLines, fmt.Sprintf("[%s] %s", l.Ts, msg))
		}

		// Track signals
		if strings.Contains(lowerMsg, "signal") || strings.Contains(lowerMsg, "breakout") || strings.Contains(lowerMsg, "trigger") || strings.Contains(lowerMsg, "setup") || strings.Contains(lowerMsg, "master") || strings.Contains(lowerMsg, "confirm") {
			var details string
			if l.Symbol != "" {
				details = fmt.Sprintf(" (%s - %s)", l.Symbol, l.Strategy)
			}
			if l.Reason != "" {
				details += fmt.Sprintf(" - Reason: %s", l.Reason)
			}
			signalLines = append(signalLines, fmt.Sprintf("[%s] %s%s", l.Ts, msg, details))
		}

		// Track orders
		if strings.Contains(lowerMsg, "order") || strings.Contains(lowerMsg, "trade") || strings.Contains(lowerMsg, "position") {
			if !strings.Contains(lowerMsg, "logger") && !strings.Contains(lowerMsg, "reconcil") {
				details := ""
				if l.Symbol != "" {
					details = fmt.Sprintf(" (%s - %s)", l.Symbol, l.Strategy)
				}
				orderLines = append(orderLines, fmt.Sprintf("[%s] %s%s", l.Ts, msg, details))
			}
		}

		// Track connections
		if strings.Contains(lowerMsg, "connect") || strings.Contains(lowerMsg, "reconnect") || strings.Contains(lowerMsg, "refused") || strings.Contains(lowerMsg, "websocket") {
			connectionLines = append(connectionLines, fmt.Sprintf("[%s] %s", l.Ts, msg))
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Printf("Scanner error: %v\n", err)
	}

	fmt.Printf("\n=== LOG ANALYSIS REPORT FOR TODAY'S BOT RUN ===\n")
	fmt.Printf("Total parsed JSON lines: %d / %d raw lines\n", lineCount, lineCount)
	fmt.Printf("\nLog Levels Counts:\n")
	for lvl, count := range levelCounts {
		fmt.Printf("  - %s: %d\n", strings.ToUpper(lvl), count)
	}

	fmt.Printf("\n--- Top Startup Events ---\n")
	printLimited(startupLines, 10)

	fmt.Printf("\n--- Connection / Websocket Events ---\n")
	printLimited(connectionLines, 10)

	fmt.Printf("\n--- Watchlist / Stock Selection Events ---\n")
	printLimited(watchlistLines, 20)

	fmt.Printf("\n--- Market Bias Events ---\n")
	printLimited(biasLines, 10)

	fmt.Printf("\n--- Breakout Setup, Trigger & Signal Events ---\n")
	printLimited(signalLines, 50)

	fmt.Printf("\n--- Order / Trade Execution Events ---\n")
	printLimited(orderLines, 30)

	fmt.Printf("\n--- Errors Summary (%d unique errors) ---\n", len(uniqueErrors))
	for errStr, count := range uniqueErrors {
		fmt.Printf("  - [x%d] %s\n", count, errStr)
	}

	fmt.Printf("\n--- Warnings Summary (%d unique warnings, top 15 shown) ---\n", len(uniqueWarnings))
	wCount := 0
	for warnStr, count := range uniqueWarnings {
		if wCount >= 15 {
			break
		}
		fmt.Printf("  - [x%d] %s\n", count, warnStr)
		wCount++
	}
}

func printLimited(lines []string, limit int) {
	if len(lines) == 0 {
		fmt.Println("  (No events recorded)")
		return
	}
	if len(lines) <= limit {
		for _, line := range lines {
			fmt.Printf("  %s\n", line)
		}
	} else {
		// Show first few and last few
		half := limit / 2
		for i := 0; i < half; i++ {
			fmt.Printf("  %s\n", lines[i])
		}
		fmt.Printf("  ... [skipped %d lines] ...\n", len(lines)-limit)
		for i := len(lines) - half; i < len(lines); i++ {
			fmt.Printf("  %s\n", lines[i])
		}
	}
}
