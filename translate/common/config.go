package common

type FailoverConfig struct {
	// Disable translator temporality for CooldownBaseSec * failureCount
	// if reached MaxFailures, set MaxFailures to 1
	// to disable a failed translator immediately
	MaxFailures     int `yaml:"max_failures"`
	CooldownBaseSec int `yaml:"cooldown_base_sec"`

	// Disable translator permanently if failure counts reached MaxDisableCycles
	MaxDisableCycles int `yaml:"max_disable_cycles"`
}

// RateLimitConfig defines the parameters for the rate limiter.
type RateLimitConfig struct {
	Enabled    bool    `yaml:"enabled"`
	BucketSize int     `yaml:"bucket_size"`
	RefillTPS  float64 `yaml:"refill_token_per_sec"`
}
