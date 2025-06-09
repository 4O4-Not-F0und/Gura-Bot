package common

import (
	"fmt"

	"github.com/sirupsen/logrus"
	"golang.org/x/time/rate"
)

type FailoverConfig struct {
	// Disable componment temporality for CooldownBaseSec * failureCount
	// For example, if reached MaxFailures, set MaxFailures to 1
	// to disable a failed componment immediately
	MaxFailures     int `yaml:"max_failures,omitempty"`
	CooldownBaseSec int `yaml:"cooldown_base_sec,omitempty"`

	// Disable componment permanently if failure counts reached MaxDisableCycles
	MaxDisableCycles int `yaml:"max_disable_cycles,omitempty"`
}

func (fc *FailoverConfig) SetDefault() {
	// By default config, will disable componments consistely fail for:
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

func (fc *FailoverConfig) CheckAndMerge(cfg FailoverConfig) (err error) {
	if fc.MaxFailures < 1 {
		fc.MaxFailures = cfg.MaxFailures
	}

	if fc.CooldownBaseSec <= 0 {
		fc.CooldownBaseSec = cfg.CooldownBaseSec
		if fc.CooldownBaseSec <= 0 {
			err = fmt.Errorf("the failover cooldown must be positive")
			return
		}
	}

	if fc.MaxDisableCycles < 1 {
		fc.MaxDisableCycles = cfg.MaxDisableCycles
	}
	if fc.MaxDisableCycles <= 1 {
		logrus.Warnf(
			"you set the failover max disable cycles as %d, which might causes component will be DISABLED PERMANENTLY IF ANY FAILURE OCCURRED",
			fc.MaxDisableCycles)
	}
	return
}

// RateLimitConfig defines the parameters for the rate limiter.
type RateLimitConfig struct {
	Enabled    bool    `yaml:"enabled"`
	BucketSize int     `yaml:"bucket_size"`
	RefillTPS  float64 `yaml:"refill_token_per_sec"`
}

func (rlc *RateLimitConfig) Check() (err error) {
	if rlc.Enabled {
		if rlc.RefillTPS <= 0.0 {
			err = fmt.Errorf("limiter refill rate must be positive")
			return
		}

		if rlc.BucketSize <= 0 {
			err = fmt.Errorf("limiter bucket size must be positive")
			return
		}
	}
	return
}

func (rlc *RateLimitConfig) NewLimiterFromConfig(logger *logrus.Entry) *rate.Limiter {
	if !rlc.Enabled {
		return nil
	}
	logger.Debugf(
		"rate limiter refill: %.2f tokens/s, bucket size: %d",
		rlc.RefillTPS, rlc.BucketSize,
	)
	return rate.NewLimiter(rate.Limit(rlc.RefillTPS), rlc.BucketSize)
}
