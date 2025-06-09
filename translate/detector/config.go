package detector

import (
	"fmt"

	"github.com/4O4-Not-F0und/Gura-Bot/translate/common"
)

type DefaultDetectorConfig struct {
	// Positive
	Weight int `yaml:"weight"`

	// A list of ISO 639-1 language codes that should be configured to detect.
	DetectLangs []string `yaml:"detect_langs"`

	// Minimum confidence score required for a detected language to be
	// considered valid by this detector.
	SourceLangConfidenceThreshold float64 `yaml:"source_lang_confidence_threshold"`

	// A list of ISO 639-1 language codes that this detector will report as valid.
	SourceLangFilter []string `yaml:"source_lang_filter"`

	// Optional. Failover
	Failover common.FailoverConfig `yaml:"failover,omitempty"`
}

type DetectorConfig struct {
	DefaultDetectorConfig `yaml:",inline"`

	// Required
	Name string `yaml:"name"`

	// Required
	Type string `yaml:"type"`

	// Positive
	Timeout int64 `yaml:"timeout"`

	// Required
	Endpoint string `yaml:"endpoint"`

	// Optional
	Token string `yaml:"token"`

	// Optional
	RateLimit common.RateLimitConfig `yaml:"rate_limit"`
}

func (tic *DetectorConfig) CheckAndMergeDefaultConfig(dtc DefaultDetectorConfig) (err error) {
	if tic.Name == "" {
		err = fmt.Errorf("detector name is required")
		return
	}

	if tic.Type == "" {
		err = fmt.Errorf("%s: type is required", tic.Name)
		return
	}

	if tic.Weight <= 0 {
		if dtc.Weight <= 0 {
			err = fmt.Errorf("%s: weight must be positive", tic.Name)
			return
		}
		tic.Weight = dtc.Weight
	}

	if tic.Timeout <= 0 {
		err = fmt.Errorf("%s: timeout must be positive", tic.Name)
		return
	}

	/*
		if tic.Endpoint == "" {
			err = fmt.Errorf("%s: endpoint is required", tic.Name)
			return
		}
	*/

	if len(tic.DetectLangs) == 0 {
		tic.DetectLangs = dtc.DetectLangs
	}
	if len(tic.DetectLangs) == 0 {
		err = fmt.Errorf("%s: no detect languages configured", tic.Name)
		return
	}

	if len(tic.SourceLangFilter) == 0 {
		tic.SourceLangFilter = dtc.SourceLangFilter
	}
	if len(tic.SourceLangFilter) == 0 {
		err = fmt.Errorf("%s: no source language filter configured", tic.Name)
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
