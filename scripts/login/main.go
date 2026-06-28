package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"

	kiteconnect "github.com/zerodha/gokiteconnect/v4"
	"zerodha-trading/config"
)

func main() {
	// Load config
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	requestToken := cfg.AccessToken // It's currently stored in this field in .env
	if len(os.Args) > 1 {
		requestToken = os.Args[1]
	}

	if requestToken == "" || requestToken == "your_access_token_here" {
		log.Fatalf("No request token provided. Run: go run scripts/login/main.go <request_token>")
	}

	log.Printf("Exchanging request token '%s' for a live Zerodha access token...", requestToken)

	// Create Kite Client
	kiteClient := kiteconnect.New(cfg.APIKey)

	// Generate session
	session, err := kiteClient.GenerateSession(requestToken, cfg.APISecret)
	if err != nil {
		log.Fatalf("Failed to generate session: %v. Make sure the request token is fresh (valid for a few minutes) and API key/secret are correct.", err)
	}

	log.Printf("Successfully generated session! User: %s (ID: %s)", session.UserName, session.UserID)
	log.Printf("Access Token: %s", session.AccessToken)

	// Update .env file
	envBytes, err := ioutil.ReadFile(".env")
	if err != nil {
		log.Fatalf("Failed to read .env file: %v", err)
	}

	lines := strings.Split(string(envBytes), "\n")
	updated := false
	for i, line := range lines {
		// Clean up carriage returns from Windows style line endings if present
		cleanLine := strings.TrimRight(line, "\r")
		if strings.HasPrefix(cleanLine, "KITE_ACCESS_TOKEN=") {
			lines[i] = fmt.Sprintf("KITE_ACCESS_TOKEN=%s", session.AccessToken)
			updated = true
			break
		}
	}

	if !updated {
		lines = append(lines, fmt.Sprintf("KITE_ACCESS_TOKEN=%s", session.AccessToken))
	}

	err = ioutil.WriteFile(".env", []byte(strings.Join(lines, "\n")), 0644)
	if err != nil {
		log.Fatalf("Failed to write updated .env file: %v", err)
	}

	log.Println("Successfully updated KITE_ACCESS_TOKEN in .env file! You can now run the seeder or bot.")
}
