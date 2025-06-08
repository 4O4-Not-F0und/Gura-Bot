package translate

import (
	"github.com/4O4-Not-F0und/Gura-Bot/translate/detector"
	"github.com/4O4-Not-F0und/Gura-Bot/translate/translator"
)

// TranslateConfig holds all configuration related to translation services.
type TranslateServiceConfig struct {
	MaximumRetry             int                                `yaml:"max_retry"`
	RetryCooldown            int                                `yaml:"retry_cooldown"`
	DefaultDetectorConfig    detector.DefaultDetectorConfig     `yaml:"default_detector_config"`
	LanguageDetectorSelector string                             `yaml:"language_detector_selector"`
	LanguageDetectors        []detector.DetectorConfig          `yaml:"language_detectors"`
	DefaultTranslatorConfig  translator.DefaultTranslatorConfig `yaml:"default_translator_config"`
	TranslatorSelector       string                             `yaml:"translator_selector"`
	Translators              []translator.TranslatorConfig      `yaml:"translators"`
}

// NewTranslateServiceConfig creates a new TranslateConfig with default empty slices and zero values.
func NewTranslateServiceConfig() (c TranslateServiceConfig) {
	c = TranslateServiceConfig{
		LanguageDetectors: make([]detector.DetectorConfig, 0),
		Translators:       make([]translator.TranslatorConfig, 0),
	}
	c.DefaultTranslatorConfig.Failover.SetDefault()
	c.DefaultDetectorConfig.Failover.SetDefault()
	return
}
