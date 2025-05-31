package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
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
	if err := validateHealthcheckURL(url); err != nil {
		fmt.Fprintf(stderr, "invalid MNEMONAS_HEALTHCHECK_URL %q: %v\n", url, err)
		return 2
	}

	timeout := defaultTimeout
	if raw := getenv("MNEMONAS_HEALTHCHECK_TIMEOUT"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			fmt.Fprintf(stderr, "invalid MNEMONAS_HEALTHCHECK_TIMEOUT %q: %v\n", raw, err)
			return 2
		}
		if parsed <= 0 {
			fmt.Fprintf(stderr, "invalid MNEMONAS_HEALTHCHECK_TIMEOUT %q: must be positive\n", raw)
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

func validateHealthcheckURL(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fmt.Errorf("cannot be empty")
	}
	if trimmed != raw || strings.IndexFunc(trimmed, func(r rune) bool {
		return r <= 0x20 || r == 0x7f
	}) >= 0 {
		return fmt.Errorf("must not contain whitespace or control characters")
	}

	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("must be an absolute http or https URL")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		return nil
	default:
		return fmt.Errorf("must use http or https")
	}
}
