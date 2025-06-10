package translator

import (
	"fmt"

	"github.com/4O4-Not-F0und/Gura-Bot/translate/common"
)

type DefaultTranslatorConfig struct {
	// Positive
	Weight int `yaml:"weight"`

	// Optional
	SystemPrompt string `yaml:"system_prompt"`

	// Optional. Failover
	Failover common.FailoverConfig `yaml:"failover,omitempty"`
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
	RateLimit common.RateLimitConfig `yaml:"rate_limit"`
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
		err = fmt.Errorf("%s: translator timeout must be positive", tic.Name)
		return
	}

	if tic.Endpoint == "" {
		err = fmt.Errorf("translator endpoint is required")
		return
	}

	// Failover
	err = tic.Failover.CheckAndMerge(dtc.Failover)
	if err != nil {
		err = fmt.Errorf("%s: %w", tic.Name, err)
		return
	}

	// Rate Limit
	err = tic.RateLimit.Check()
	return
}
