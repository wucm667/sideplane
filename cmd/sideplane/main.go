package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/wucm667/sideplane/internal/auth"
	"github.com/wucm667/sideplane/pkg/protocol"
)

const version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) >= 2 && args[0] == "enrollment" && args[1] == "create" {
		return runEnrollmentCreate(args[2:], stdout, stderr)
	}

	flags := flag.NewFlagSet("sideplane", flag.ContinueOnError)
	flags.SetOutput(stderr)
	showVersion := flags.Bool("version", false, "print version and exit")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	if *showVersion {
		fmt.Fprintf(stdout, "sideplane %s\n", version)
		return 0
	}

	fmt.Fprintln(stdout, "sideplane CLI skeleton")
	return 0
}

func runEnrollmentCreate(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("sideplane enrollment create", flag.ContinueOnError)
	flags.SetOutput(stderr)

	serverURL := flags.String("server", "", "Sideplane server URL")
	expiresIn := flags.Duration("expires-in", 0, "optional duration before the token expires")
	operatorTokenFlag := flags.String("operator-token", "", "operator bearer token; can also be set with SIDEPLANE_OPERATOR_TOKEN")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	operatorToken := strings.TrimSpace(*operatorTokenFlag)
	if operatorToken == "" {
		operatorToken = strings.TrimSpace(os.Getenv(auth.OperatorTokenEnv))
	}

	resp, err := createEnrollmentToken(context.Background(), *serverURL, *expiresIn, operatorToken)
	if err != nil {
		fmt.Fprintf(stderr, "create enrollment token: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "enrollment token: %s\n", resp.Token)
	fmt.Fprintf(stdout, "expires at: %s\n", resp.ExpiresAt.Format(time.RFC3339))
	return 0
}

func createEnrollmentToken(ctx context.Context, serverURL string, expiresIn time.Duration, operatorToken string) (protocol.CreateEnrollmentTokenResponse, error) {
	endpoint, err := apiEndpoint(serverURL, "/api/enrollment-tokens")
	if err != nil {
		return protocol.CreateEnrollmentTokenResponse{}, err
	}

	var body bytes.Buffer
	if expiresIn > 0 {
		if err := json.NewEncoder(&body).Encode(protocol.CreateEnrollmentTokenRequest{
			ExpiresAt: time.Now().UTC().Add(expiresIn),
		}); err != nil {
			return protocol.CreateEnrollmentTokenResponse{}, fmt.Errorf("encode enrollment token request: %w", err)
		}
	} else {
		body.WriteString("{}")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return protocol.CreateEnrollmentTokenResponse{}, fmt.Errorf("create enrollment token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if token := strings.TrimSpace(operatorToken); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	httpClient := &http.Client{Timeout: 10 * time.Second}
	httpResp, err := httpClient.Do(req)
	if err != nil {
		return protocol.CreateEnrollmentTokenResponse{}, fmt.Errorf("post enrollment token request: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode < http.StatusOK || httpResp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(httpResp.Body, 1024))
		return protocol.CreateEnrollmentTokenResponse{}, fmt.Errorf("server returned status %d: %s", httpResp.StatusCode, strings.TrimSpace(string(body)))
	}

	var resp protocol.CreateEnrollmentTokenResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return protocol.CreateEnrollmentTokenResponse{}, fmt.Errorf("decode enrollment token response: %w", err)
	}
	if strings.TrimSpace(resp.Token) == "" || resp.ExpiresAt.IsZero() {
		return protocol.CreateEnrollmentTokenResponse{}, fmt.Errorf("server response missing token or expiry")
	}
	return resp, nil
}

func apiEndpoint(serverURL string, path string) (string, error) {
	if strings.TrimSpace(serverURL) == "" {
		return "", fmt.Errorf("server URL is required")
	}
	serverURL = strings.TrimSpace(serverURL)
	if !strings.Contains(serverURL, "://") {
		serverURL = "http://" + serverURL
	}
	parsed, err := url.Parse(serverURL)
	if err != nil {
		return "", fmt.Errorf("parse server URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("server URL must be absolute: %q", serverURL)
	}
	endpoint, err := url.JoinPath(strings.TrimRight(serverURL, "/"), path)
	if err != nil {
		return "", fmt.Errorf("build API endpoint: %w", err)
	}
	return endpoint, nil
}
