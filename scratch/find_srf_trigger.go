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
	if err != nil && stderr.Len() == 0 {
		log.Fatalf("Failed to run docker logs: %v", err)
	}

	logData := stdout.String() + stderr.String()
	scanner := bufio.NewScanner(strings.NewReader(logData))
	
	fmt.Println("=== SRF Trigger and Setup Logs ===")
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "SRF") && !strings.Contains(line, "packet loss") {
			fmt.Println(line)
		}
	}
}
