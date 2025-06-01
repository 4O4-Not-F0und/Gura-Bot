package main

import "fmt"

const (
	translatorInstanceTypeOpenAI = "openai"
)

// TranslateConfig holds all configuration related to translation services.
type TranslateServiceConfig struct {
	DetectLangs             []string                   `yaml:"detect_langs"`
	SourceLang              SourceLanguageConfig       `yaml:"source_lang"`
	Translators             []TranslatorInstanceConfig `yaml:"translators"`
	DefaultTranslatorConfig DefaultTranslatorConfig    `yaml:"default_translator_config"`
	GlobalRateLimit         TranslateRateLimitConfig   `yaml:"rate_limit"`
}

// TranslateRateLimitConfig defines the parameters for the rate limiter.
type TranslateRateLimitConfig struct {
	BucketSize int     `yaml:"bucket_size"`
	RefillTPS  float64 `yaml:"refill_token_per_sec"`
}

// SourceLanguageConfig defines parameters for validating detected source languages.
type SourceLanguageConfig struct {
	ConfidenceThreshold float64  `yaml:"confidence_threshold"`
	Langs               []string `yaml:"langs"`
}

// newTranslateConfig creates a new TranslateConfig with default empty slices and zero values.
func newTranslateServiceConfig() (c TranslateServiceConfig) {
	return TranslateServiceConfig{
		DetectLangs: make([]string, 0),
		SourceLang: SourceLanguageConfig{
			ConfidenceThreshold: 0,
			Langs:               make([]string, 0),
		},
		Translators: make([]TranslatorInstanceConfig, 0),
	}
}

type DefaultTranslatorConfig struct {
	// Required
	Type string `yaml:"type"`

	// Postive
	Weight int `yaml:"weight"`

	// Optional
	Model string `yaml:"model"`

	// Optional
	SystemPrompt string `yaml:"system_prompt"`

	// Postive
	Timeout int64 `yaml:"timeout"`

	// Required
	Endpoint string `yaml:"endpoint"`
}

type TranslatorInstanceConfig struct {
	DefaultTranslatorConfig `yaml:",inline"`

	// Required
	Name string `yaml:"name"`

	// Optional
	Token string `yaml:"token"`
}

func (tic *TranslatorInstanceConfig) CheckAndMergeDefaultConfig(dtc DefaultTranslatorConfig) (err error) {
	if tic.Name == "" {
		err = fmt.Errorf("translator name is required")
		return
	}

	if tic.Type == "" {
		if dtc.Type == "" {
			err = fmt.Errorf("translator type is required")
			return
		}
		tic.Type = dtc.Type
	}

	if tic.Weight <= 0 {
		if dtc.Weight <= 0 {
			err = fmt.Errorf("translator weight must be positive")
			return
		}
		tic.Weight = dtc.Weight
	}

	if tic.Model == "" {
		tic.Model = dtc.Model
	}

	if tic.SystemPrompt == "" {
		tic.SystemPrompt = dtc.SystemPrompt
	}

	if tic.Timeout <= 0 {
		if dtc.Timeout <= 0 {
			err = fmt.Errorf("translator timeout must be positive")
			return
		}
		tic.Timeout = dtc.Timeout
	}

	if tic.Endpoint == "" {
		tic.Endpoint = dtc.Endpoint
	}
	return
}
