package main

import (
	"fmt"

	"github.com/sirupsen/logrus"
)

const (
	translatorInstanceTypeOpenAI = "openai"
)

// TranslateConfig holds all configuration related to translation services.
type TranslateServiceConfig struct {
	MaxmiumRetry            int                        `yaml:"max_retry"`
	RetryCooldown           int                        `yaml:"retry_cooldown"`
	DetectLangs             []string                   `yaml:"detect_langs"`
	SourceLang              SourceLanguageConfig       `yaml:"source_lang"`
	Translators             []TranslatorInstanceConfig `yaml:"translators"`
	DefaultTranslatorConfig DefaultTranslatorConfig    `yaml:"default_translator_config"`
}

// SourceLanguageConfig defines parameters for validating detected source languages.
type SourceLanguageConfig struct {
	ConfidenceThreshold float64  `yaml:"confidence_threshold"`
	Langs               []string `yaml:"langs"`
}

// newTranslateConfig creates a new TranslateConfig with default empty slices and zero values.
func newTranslateServiceConfig() (c TranslateServiceConfig) {
	c = TranslateServiceConfig{
		DetectLangs: make([]string, 0),
		SourceLang: SourceLanguageConfig{
			ConfidenceThreshold: 0,
			Langs:               make([]string, 0),
		},
		Translators: make([]TranslatorInstanceConfig, 0),
	}

	// By default config, will disable translators consistely fail for:
	// 1  failure:  no cooldown
	// 2  failures: no cooldown
	// 3  failures: 1 * 120 secs cooldown
	// 6  failures: 2 * 120 secs cooldown
	// 9  failures: 3 * 120 secs cooldown
	// 12 failures: 4 * 120 secs cooldown
	// 15 failures: 5 * 120 secs cooldown
	// 18 failures: disable it until next config reloading or restarting
	c.DefaultTranslatorConfig.Failover.MaxFailures = 3
	c.DefaultTranslatorConfig.Failover.CooldownBaseSec = 120
	c.DefaultTranslatorConfig.Failover.MaxDisableCycles = 6

	return
}

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

type DefaultTranslatorConfig struct {
	// Positive
	Weight int `yaml:"weight"`

	// Optional
	SystemPrompt string `yaml:"system_prompt"`

	// Optional. Failover
	Failover FailoverConfig `yaml:"failover"`
}

type TranslatorInstanceConfig struct {
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
	RateLimitConfig `yaml:"rate_limit"`
}

func (tic *TranslatorInstanceConfig) CheckAndMergeDefaultConfig(dtc DefaultTranslatorConfig) (err error) {
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
