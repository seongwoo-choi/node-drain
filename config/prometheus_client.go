package config

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
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
	for key, value := range h.headers {
		req.Header.Add(key, value)
	}
	return h.rt.RoundTrip(req)
}

func CreatePrometheusClient() (api.Client, error) {
	headers := map[string]string{}
	if orgID := strings.TrimSpace(os.Getenv("PROMETHEUS_SCOPE_ORG_ID")); orgID != "" {
		headers["X-Scope-OrgID"] = orgID
	}

	config := api.Config{
		Address: os.Getenv("PROMETHEUS_ADDRESS"),
		RoundTripper: &headerRoundTripper{
			headers: headers,
			rt:      http.DefaultTransport,
		},
	}
	return api.NewClient(config)
}

func QueryPrometheus(client api.Client, query string) (model.Vector, error) {
	return QueryPrometheusWithContext(context.Background(), client, query)
}

func QueryPrometheusWithContext(ctx context.Context, client api.Client, query string) (model.Vector, error) {
	v1api := v1.NewAPI(client)
	if ctx == nil {
		ctx = context.Background()
	}
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
