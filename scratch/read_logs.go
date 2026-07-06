package main

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"os/exec"
	"strings"
)

func main() {
	cmd := exec.Command("docker", "logs", "zt-app-1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		// Sometimes docker logs outputs to stderr
		if stderr.Len() == 0 {
			log.Fatalf("Failed to run docker logs: %v", err)
		}
	}

	// Combine stdout and stderr because docker logs often writes to stderr
	logData := stdout.String() + stderr.String()

	scanner := bufio.NewScanner(strings.NewReader(logData))
	fmt.Println("=== Cleaned Bot Logs (excluding packet loss warnings) ===")
	
	for scanner.Scan() {
		line := scanner.Text()
		
		// Skip packet loss warnings
		if strings.Contains(line, "Potential packet loss") || 
		   strings.Contains(line, "Catching up historical") ||
		   strings.Contains(line, "packet loss") ||
		   strings.Contains(line, "Subscribe") {
			continue
		}
		
		fmt.Println(line)
	}

	if err := scanner.Err(); err != nil {
		log.Fatalf("Error reading log data: %v", err)
	}
}
