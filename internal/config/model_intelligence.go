package config

// ModelIntelligenceConfig configures the management model benchmark feed backed
// by the Artificial Analysis Data API.
type ModelIntelligenceConfig struct {
	// ArtificialAnalysisAPIKey authenticates Artificial Analysis requests. When empty,
	// the ARTIFICIAL_ANALYSIS_API_KEY environment variable is used instead.
	ArtificialAnalysisAPIKey string `yaml:"artificial-analysis-api-key,omitempty" json:"artificial-analysis-api-key,omitempty"`

	// ArtificialAnalysisTier selects the endpoint: "free" (default) or "pro".
	ArtificialAnalysisTier string `yaml:"artificial-analysis-tier,omitempty" json:"artificial-analysis-tier,omitempty"`

	// CacheTTL is a Go duration string controlling how long fetched data is reused.
	// Default: 6h.
	CacheTTL string `yaml:"cache-ttl,omitempty" json:"cache-ttl,omitempty"`
}
