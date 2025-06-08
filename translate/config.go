package translate

import "github.com/4O4-Not-F0und/Gura-Bot/translate/translator"

// TranslateConfig holds all configuration related to translation services.
type TranslateServiceConfig struct {
	MaxmiumRetry            int                                `yaml:"max_retry"`
	RetryCooldown           int                                `yaml:"retry_cooldown"`
	DetectLangs             []string                           `yaml:"detect_langs"`
	SourceLang              SourceLanguageConfig               `yaml:"source_lang"`
	TranslatorSelector      string                             `yaml:"translator_selector"`
	Translators             []translator.TranslatorConfig      `yaml:"translators"`
	DefaultTranslatorConfig translator.DefaultTranslatorConfig `yaml:"default_translator_config"`
}

// SourceLanguageConfig defines parameters for validating detected source languages.
type SourceLanguageConfig struct {
	ConfidenceThreshold float64  `yaml:"confidence_threshold"`
	Langs               []string `yaml:"langs"`
}

// NewTranslateServiceConfig creates a new TranslateConfig with default empty slices and zero values.
func NewTranslateServiceConfig() (c TranslateServiceConfig) {
	c = TranslateServiceConfig{
		DetectLangs: make([]string, 0),
		SourceLang: SourceLanguageConfig{
			ConfidenceThreshold: 0,
			Langs:               make([]string, 0),
		},
		Translators: make([]translator.TranslatorConfig, 0),
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
