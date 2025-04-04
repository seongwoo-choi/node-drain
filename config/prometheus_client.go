package config

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
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
	config := api.Config{
		Address: os.Getenv("PROMETHEUS_ADDRESS"),
		RoundTripper: &headerRoundTripper{
			headers: map[string]string{
				"X-Scope-OrgID": os.Getenv("PROMETHEUS_SCOPE_ORG_ID"),
			},
			rt: http.DefaultTransport,
		},
	}
	return api.NewClient(config)
}

func QueryPrometheus(client api.Client, query string) (model.Vector, error) {
	v1api := v1.NewAPI(client)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
