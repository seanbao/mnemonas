package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const (
	defaultHealthURL = "http://127.0.0.1:8080/health"
	defaultTimeout   = 2 * time.Second
)

func main() {
	os.Exit(run(os.Getenv, os.Stderr))
}

func run(getenv func(string) string, stderr io.Writer) int {
	url := getenv("MNEMONAS_HEALTHCHECK_URL")
	if url == "" {
		url = defaultHealthURL
	}

	timeout := defaultTimeout
	if raw := getenv("MNEMONAS_HEALTHCHECK_TIMEOUT"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			fmt.Fprintf(stderr, "invalid MNEMONAS_HEALTHCHECK_TIMEOUT %q: %v\n", raw, err)
			return 2
		}
		timeout = parsed
	}

	client := http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintf(stderr, "healthcheck request failed: %v\n", err)
		return 1
	}
	defer resp.Body.Close()

	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		fmt.Fprintf(stderr, "healthcheck returned status %s\n", resp.Status)
		return 1
	}

	return 0
}
