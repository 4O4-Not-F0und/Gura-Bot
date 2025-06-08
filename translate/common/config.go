package common

type FailoverConfig struct {
	// Disable translator temporality for CooldownBaseSec * failureCount
	// if reached MaxFailures, set MaxFailures to 1
	// to disable a failed translator immediately
	MaxFailures     int `yaml:"max_failures,omitempty"`
	CooldownBaseSec int `yaml:"cooldown_base_sec,omitempty"`

	// Disable translator permanently if failure counts reached MaxDisableCycles
	MaxDisableCycles int `yaml:"max_disable_cycles,omitempty"`
}

func (fc *FailoverConfig) SetDefault() {
	// By default config, will disable translators consistely fail for:
	// 1  failure:  no cooldown
	// 2  failures: no cooldown
	// 3  failures: 1 * 120 secs cooldown
	// 6  failures: 2 * 120 secs cooldown
	// 9  failures: 3 * 120 secs cooldown
	// 12 failures: 4 * 120 secs cooldown
	// 15 failures: 5 * 120 secs cooldown
	// 18 failures: disable it until next config reloading or restarting
	fc.MaxFailures = 3
	fc.CooldownBaseSec = 120
	fc.MaxDisableCycles = 6
}

// RateLimitConfig defines the parameters for the rate limiter.
type RateLimitConfig struct {
	Enabled    bool    `yaml:"enabled"`
	BucketSize int     `yaml:"bucket_size"`
	RefillTPS  float64 `yaml:"refill_token_per_sec"`
}
