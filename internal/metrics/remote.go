package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

var httpClient = &http.Client{Timeout: 20 * time.Second}

// Prometheus queries a Prometheus-compatible HTTP API (Prometheus, Thanos, Mimir, VictoriaMetrics).
type Prometheus struct {
	URL     string
	Queries map[string]string // metric -> PromQL with {{service}} / {{track}}
}

// Name implements Provider.
func (p *Prometheus) Name() string { return "prometheus" }

// Sample implements Provider using /api/v1/query_range with a 15 s step.
func (p *Prometheus) Sample(ctx context.Context, service, metric string, track Track, window time.Duration) ([]float64, error) {
	q, ok := p.Queries[metric]
	if !ok {
		return nil, fmt.Errorf("prometheus: no query configured for %q", metric)
	}
	end := time.Now()
	v := url.Values{
		"query": {render(q, service, track)},
		"start": {strconv.FormatInt(end.Add(-window).Unix(), 10)},
		"end":   {strconv.FormatInt(end.Unix(), 10)},
		"step":  {"15"},
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, p.URL+"/api/v1/query_range?"+v.Encode(), nil)
	var body struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Data   struct {
			Result []struct {
				Values [][2]any `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := getJSON(req, &body); err != nil {
		return nil, fmt.Errorf("prometheus: %w", err)
	}
	if body.Status != "success" {
		return nil, fmt.Errorf("prometheus: %s", body.Error)
	}
	var out []float64
	for _, series := range body.Data.Result {
		for _, pair := range series.Values {
			if s, ok := pair[1].(string); ok {
				if f, err := strconv.ParseFloat(s, 64); err == nil {
					out = append(out, f)
				}
			}
		}
	}
	return out, nil
}

// Datadog queries the Datadog metrics API. Keys come from DD_API_KEY and DD_APP_KEY.
type Datadog struct {
	Site    string // e.g. https://api.datadoghq.com
	Queries map[string]string
	apiKey  string
	appKey  string
}

// NewDatadog validates credentials from the environment.
func NewDatadog(site string, queries map[string]string) (*Datadog, error) {
	if site == "" {
		site = "https://api.datadoghq.com"
	}
	d := &Datadog{Site: site, Queries: queries, apiKey: os.Getenv("DD_API_KEY"), appKey: os.Getenv("DD_APP_KEY")}
	if d.apiKey == "" || d.appKey == "" {
		return nil, fmt.Errorf("datadog: set DD_API_KEY and DD_APP_KEY")
	}
	return d, nil
}

// Name implements Provider.
func (d *Datadog) Name() string { return "datadog" }

// Sample implements Provider using /api/v1/query.
func (d *Datadog) Sample(ctx context.Context, service, metric string, track Track, window time.Duration) ([]float64, error) {
	q, ok := d.Queries[metric]
	if !ok {
		return nil, fmt.Errorf("datadog: no query configured for %q", metric)
	}
	end := time.Now()
	v := url.Values{
		"query": {render(q, service, track)},
		"from":  {strconv.FormatInt(end.Add(-window).Unix(), 10)},
		"to":    {strconv.FormatInt(end.Unix(), 10)},
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, d.Site+"/api/v1/query?"+v.Encode(), nil)
	req.Header.Set("DD-API-KEY", d.apiKey)
	req.Header.Set("DD-APPLICATION-KEY", d.appKey)
	var body struct {
		Series []struct {
			Pointlist [][2]*float64 `json:"pointlist"`
		} `json:"series"`
	}
	if err := getJSON(req, &body); err != nil {
		return nil, fmt.Errorf("datadog: %w", err)
	}
	var out []float64
	for _, s := range body.Series {
		for _, p := range s.Pointlist {
			if p[1] != nil {
				out = append(out, *p[1])
			}
		}
	}
	return out, nil
}

func getJSON(req *http.Request, v any) error {
	res, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d from %s", res.StatusCode, req.URL.Host)
	}
	return json.NewDecoder(res.Body).Decode(v)
}
