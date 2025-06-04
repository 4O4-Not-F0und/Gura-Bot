package main

import (
	"fmt"
	"os"

	"github.com/4O4-Not-F0und/Gura-Bot/metrics"
	"github.com/4O4-Not-F0und/Gura-Bot/translate"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Bot              BotConfig                        `yaml:"bot"`
	LogLevel         string                           `yaml:"log_level"`
	TranslateService translate.TranslateServiceConfig `yaml:"translate_service"`
	Metric           metrics.MetricConfig             `yaml:"metric"`
}

func newConfig() *Config {
	return &Config{
		Bot:              newBotConfig(),
		TranslateService: translate.NewTranslateServiceConfig(),
	}
}

func loadConfig(configFile string) (cfg *Config, err error) {

	cfg = newConfig()
	yamlFile, err := os.ReadFile(configFile)
	if err != nil {
		if os.IsNotExist(err) {
			err = fmt.Errorf("config file '%s' not found", configFile)
			return
		}
		return nil, fmt.Errorf("read config file '%s' failed: %w", configFile, err)
	}

	err = yaml.Unmarshal(yamlFile, &cfg)
	if err != nil {
		return nil, fmt.Errorf("parse '%s' failed: %w", configFile, err)
	}
	return
}
