package translator

import (
	"fmt"

	"github.com/4O4-Not-F0und/Gura-Bot/translate/common"
	"github.com/sirupsen/logrus"
)

type DefaultTranslatorConfig struct {
	// Positive
	Weight int `yaml:"weight"`

	// Optional
	SystemPrompt string `yaml:"system_prompt"`

	// Optional. Failover
	Failover common.FailoverConfig `yaml:"failover"`
}

type TranslatorConfig struct {
	DefaultTranslatorConfig `yaml:",inline"`

	// Required
	Name string `yaml:"name"`

	// Required
	Type string `yaml:"type"`

	// Positive
	Timeout int64 `yaml:"timeout"`

	// Optional
	Model string `yaml:"model"`

	// Required
	Endpoint string `yaml:"endpoint"`

	// Optional
	Token string `yaml:"token"`

	// Optional
	common.RateLimitConfig `yaml:"rate_limit"`
}

func (tic *TranslatorConfig) CheckAndMergeDefaultConfig(dtc DefaultTranslatorConfig) (err error) {
	if tic.Name == "" {
		err = fmt.Errorf("translator name is required")
		return
	}

	if tic.Type == "" {
		err = fmt.Errorf("translator type is required")
		return
	}

	if tic.Weight <= 0 {
		if dtc.Weight <= 0 {
			err = fmt.Errorf("translator weight must be positive")
			return
		}
		tic.Weight = dtc.Weight
	}

	if tic.SystemPrompt == "" {
		tic.SystemPrompt = dtc.SystemPrompt
	}

	if tic.Timeout <= 0 {
		err = fmt.Errorf("translator timeout must be positive")
		return
	}

	if tic.Endpoint == "" {
		err = fmt.Errorf("translator endpoint is required")
		return
	}

	// Failover
	if tic.Failover.MaxFailures < 1 {
		tic.Failover.MaxFailures = dtc.Failover.MaxFailures
	}

	if tic.Failover.CooldownBaseSec <= 0 {
		tic.Failover.CooldownBaseSec = dtc.Failover.CooldownBaseSec
		if tic.Failover.CooldownBaseSec <= 0 {
			err = fmt.Errorf("the failover cooldown must be positive")
			return
		}
	}

	if tic.Failover.MaxDisableCycles < 1 {
		tic.Failover.MaxDisableCycles = dtc.Failover.MaxDisableCycles
	}
	if tic.Failover.MaxDisableCycles <= 1 {
		logrus.Warnf(
			"you set the failover max disable cycles as %d, which might causes translator will be DISABLED PERMANENTLY IF ANY FAILURE OCCURRED",
			tic.Failover.MaxDisableCycles)
	}

	// Rate Limit
	if tic.RateLimitConfig.Enabled {
		if tic.RateLimitConfig.RefillTPS <= 0.0 {
			err = fmt.Errorf("translator limiter refill rate must be positive")
			return
		}

		if tic.RateLimitConfig.BucketSize <= 0 {
			err = fmt.Errorf("translator limiter bucket size must be positive")
			return
		}
	}
	return
}
