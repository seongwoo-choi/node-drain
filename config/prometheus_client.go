package config

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

type headerRoundTripper struct {
	headers map[string]string
	rt      http.RoundTripper
}

func (h *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, errors.New("prometheus request is required")
	}

	rt := http.DefaultTransport
	if h != nil && h.rt != nil {
		rt = h.rt
	}

	if h != nil {
		for key, value := range h.headers {
			req.Header.Set(key, value)
		}
	}

	return rt.RoundTrip(req)
}

func CreatePrometheusClient() (api.Client, error) {
	address := strings.TrimSpace(os.Getenv("PROMETHEUS_ADDRESS"))
	if err := ValidatePrometheusAddress(address); err != nil {
		return nil, err
	}

	headers := map[string]string{}
	if tenantID := prometheusTenantIDFromEnv(); tenantID != "" {
		headers["X-Scope-OrgID"] = tenantID
	}

	config := api.Config{
		Address: address,
		RoundTripper: &headerRoundTripper{
			headers: headers,
			rt:      http.DefaultTransport,
		},
	}
	return api.NewClient(config)
}

func prometheusTenantIDFromEnv() string {
	if tenantID := strings.TrimSpace(os.Getenv("PROMETHEUS_TENANT_ID")); tenantID != "" {
		return tenantID
	}
	return strings.TrimSpace(os.Getenv("PROMETHEUS_SCOPE_ORG_ID"))
}

// ValidatePrometheusAddress verifies that the configured address is the Prometheus base URL.
func ValidatePrometheusAddress(address string) error {
	address = strings.TrimSpace(address)
	if address == "" {
		return errors.New("PROMETHEUS_ADDRESS is required")
	}

	parsed, err := url.Parse(address)
	if err != nil {
		return fmt.Errorf("invalid PROMETHEUS_ADDRESS: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("invalid PROMETHEUS_ADDRESS scheme: %s", parsed.Scheme)
	}
	if parsed.Host == "" {
		return errors.New("invalid PROMETHEUS_ADDRESS: host is required")
	}

	path := strings.TrimRight(parsed.EscapedPath(), "/")
	if path == "/api/v1" || strings.HasSuffix(path, "/api/v1") || strings.Contains(path, "/api/v1/") {
		return errors.New("invalid PROMETHEUS_ADDRESS: use the Prometheus base URL, not an /api/v1 endpoint")
	}
	return nil
}

func QueryPrometheus(client api.Client, query string) (model.Vector, error) {
	return QueryPrometheusWithContext(context.Background(), client, query)
}

func QueryPrometheusWithContext(ctx context.Context, client api.Client, query string) (model.Vector, error) {
	if client == nil {
		return nil, errors.New("prometheus client is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	v1api := v1.NewAPI(client)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	result, warnings, err := v1api.Query(ctx, query, time.Now())
	if err != nil {
		return nil, err
	}
	if len(warnings) > 0 {
		slog.Warn("프로메테우스를 쿼리하는 중에 오류가 발생했습니다.", "warnings", warnings)
	}

	vector, ok := result.(model.Vector)
	if !ok {
		return nil, errors.New("unexpected result type from Prometheus")
	}

	return vector, nil
}
