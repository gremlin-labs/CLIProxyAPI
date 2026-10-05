package management

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/modelintel"
)

const testAAKey = "aa-secret-key-do-not-leak"

type fakeArtificialAnalysis struct {
	calls  atomic.Int32
	status atomic.Int32
}

func (f *fakeArtificialAnalysis) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.calls.Add(1)
	if r.Header.Get("x-api-key") != testAAKey {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch status := int(f.status.Load()); status {
	case 0, http.StatusOK:
	case http.StatusTooManyRequests:
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(status)
		return
	default:
		w.WriteHeader(status)
		return
	}
	_, _ = w.Write([]byte(`{"tier":"free","intelligence_index_version":4,"pagination":{"page":1,"page_size":1,"total_pages":1,"has_more":false},
"data":[{"id":"m1","name":"Model One","slug":"model-one","model_creator":{"name":"Acme"},"reasoning_model":true,
"evaluations":{"artificial_analysis_intelligence_index":60},"pricing":{"price_1m_input_tokens":1,"price_1m_output_tokens":5}}]}`))
}

func newModelIntelHandler(t *testing.T, key string) (*Handler, *fakeArtificialAnalysis) {
	t.Helper()
	t.Setenv(modelintel.EnvAPIKey, "")
	fake := &fakeArtificialAnalysis{}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	h := &Handler{
		cfg:            &config.Config{ModelIntelligence: config.ModelIntelligenceConfig{ArtificialAnalysisAPIKey: key}},
		configFilePath: writeTestConfigFile(t),
		modelIntel:     modelintel.NewService(srv.URL, nil),
	}
	return h, fake
}

func doModelIntel(t *testing.T, h *Handler, query string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/model-intelligence"+query, nil)
	h.GetModelIntelligence(ctx)
	if strings.Contains(rec.Body.String(), testAAKey) {
		t.Fatalf("response leaked the API key: %s", rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return rec, body
}

func TestModelIntelligenceNoKey(t *testing.T) {
	h, fake := newModelIntelHandler(t, "")
	rec, body := doModelIntel(t, h, "")
	if rec.Code != http.StatusOK || body["configured"] != false || body["key_source"] != "" || fake.calls.Load() != 0 {
		t.Fatalf("status=%d body=%v calls=%d", rec.Code, body, fake.calls.Load())
	}
	if models, ok := body["models"].([]any); !ok || len(models) != 0 {
		t.Fatalf("models = %v", body["models"])
	}
	source := body["source"].(map[string]any)
	if source["name"] != "Artificial Analysis" || source["url"] != "https://artificialanalysis.ai" {
		t.Fatalf("source = %v", source)
	}
	if body["fetched_at"] != nil || body["intelligence_index_version"] != nil {
		t.Fatalf("unfetched metadata must be null: %v", body)
	}
}

func TestModelIntelligenceEnvKeyAndCache(t *testing.T) {
	h, fake := newModelIntelHandler(t, "")
	t.Setenv(modelintel.EnvAPIKey, testAAKey)
	rec, body := doModelIntel(t, h, "")
	if rec.Code != http.StatusOK || body["configured"] != true || body["key_source"] != "env" || body["cached"] != false {
		t.Fatalf("status=%d body=%v", rec.Code, body)
	}
	if body["tier"] != "free" || body["intelligence_index_version"] != float64(4) || body["fetched_at"] == nil {
		t.Fatalf("metadata = %v", body)
	}
	model := body["models"].([]any)[0].(map[string]any)
	if model["id"] != "m1" || model["intelligence_index"] != float64(60) || model["price_blended"] != float64(2) || model["coding_index"] != nil {
		t.Fatalf("model = %v", model)
	}
	if _, body = doModelIntel(t, h, ""); body["cached"] != true || fake.calls.Load() != 1 {
		t.Fatalf("second request should hit the cache: %v calls=%d", body["cached"], fake.calls.Load())
	}
	if _, body = doModelIntel(t, h, "?refresh=1"); body["cached"] != false || fake.calls.Load() != 2 {
		t.Fatalf("refresh=1 should bypass the cache: %v calls=%d", body["cached"], fake.calls.Load())
	}
}

func TestModelIntelligenceUpstreamErrors(t *testing.T) {
	h, _ := newModelIntelHandler(t, "wrong-key")
	rec, body := doModelIntel(t, h, "")
	if rec.Code != http.StatusBadGateway || body["error"] != "invalid or unauthorized Artificial Analysis API key" || body["upstream_status"] != float64(401) {
		t.Fatalf("401: status=%d body=%v", rec.Code, body)
	}

	h, fake := newModelIntelHandler(t, testAAKey)
	fake.status.Store(http.StatusTooManyRequests)
	rec, body = doModelIntel(t, h, "")
	if rec.Code != http.StatusTooManyRequests || body["retry_after"] != float64(120) || body["upstream_status"] != float64(429) {
		t.Fatalf("429 without cache: status=%d body=%v", rec.Code, body)
	}

	fake.status.Store(http.StatusOK)
	doModelIntel(t, h, "")
	fake.status.Store(http.StatusTooManyRequests)
	rec, body = doModelIntel(t, h, "?refresh=1")
	if rec.Code != http.StatusOK || body["cached"] != true || body["warning"] == nil || body["retry_after"] != float64(120) || len(body["models"].([]any)) != 1 {
		t.Fatalf("429 with cache: status=%d body=%v", rec.Code, body)
	}

	fake.status.Store(http.StatusInternalServerError)
	rec, body = doModelIntel(t, h, "?refresh=1")
	if rec.Code != http.StatusBadGateway || body["upstream_status"] != float64(500) {
		t.Fatalf("500: status=%d body=%v", rec.Code, body)
	}
}

func putModelIntelConfig(t *testing.T, h *Handler, payload string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/model-intelligence/config", strings.NewReader(payload))
	ctx.Request.Header.Set("Content-Type", "application/json")
	h.PutModelIntelligenceConfig(ctx)
	if strings.Contains(rec.Body.String(), testAAKey) {
		t.Fatalf("PUT response leaked the API key: %s", rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec, body
}

func TestPutModelIntelligenceConfigSavesAndClears(t *testing.T) {
	h, fake := newModelIntelHandler(t, "")
	rec, body := putModelIntelConfig(t, h, `{"api_key":"`+testAAKey+`","tier":"pro","cache_ttl":"2h"}`)
	if rec.Code != http.StatusOK || body["configured"] != true || body["key_source"] != "config" || body["cache_ttl"] != "2h0m0s" {
		t.Fatalf("status=%d body=%v", rec.Code, body)
	}
	saved, err := os.ReadFile(h.configFilePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), "artificial-analysis-api-key: "+testAAKey) || !strings.Contains(string(saved), "artificial-analysis-tier: pro") {
		t.Fatalf("config file not updated:\n%s", saved)
	}

	_, got := doModelIntel(t, h, "")
	if got["configured"] != true || got["configured_tier"] != "pro" || fake.calls.Load() != 1 {
		t.Fatalf("GET after PUT = %v calls=%d", got, fake.calls.Load())
	}
	// A PUT clears the cache, so the next GET fetches again.
	putModelIntelConfig(t, h, `{"tier":"free"}`)
	if _, got = doModelIntel(t, h, ""); got["cached"] != false || fake.calls.Load() != 2 {
		t.Fatalf("cache not cleared: %v calls=%d", got["cached"], fake.calls.Load())
	}

	rec, body = putModelIntelConfig(t, h, `{"api_key":""}`)
	if rec.Code != http.StatusOK || body["configured"] != false {
		t.Fatalf("clear: status=%d body=%v", rec.Code, body)
	}
	saved, err = os.ReadFile(h.configFilePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "artificial-analysis-api-key") {
		t.Fatalf("cleared key still in config:\n%s", saved)
	}
	if _, got = doModelIntel(t, h, ""); got["configured"] != false || len(got["models"].([]any)) != 0 {
		t.Fatalf("GET after clear = %v", got)
	}
}

func TestPutModelIntelligenceConfigValidates(t *testing.T) {
	h, _ := newModelIntelHandler(t, "")
	for _, payload := range []string{`{"tier":"enterprise"}`, `{"cache_ttl":"soon"}`, `{"cache_ttl":"-1h"}`, `not json`} {
		if rec, _ := putModelIntelConfig(t, h, payload); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status=%d", payload, rec.Code)
		}
	}
}
