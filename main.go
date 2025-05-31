package main

import (
	"flag"
	"os"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	defaultConfigFile = "config.yml"
)

var (
	configFile = defaultConfigFile
)

func init() {
	flag.StringVar(&configFile, "config", defaultConfigFile, "path to config file")
	flag.Parse()

	logrus.SetOutput(os.Stdout)
	logrus.SetFormatter(&logrus.TextFormatter{
		TimestampFormat:        time.RFC3339,
		DisableColors:          true,
		DisableLevelTruncation: true,
		ForceQuote:             true,
		FullTimestamp:          true,
	})

}

func main() {
	appConfig, err := loadConfig(configFile)
	if err != nil {
		logrus.Fatalf("load config failed: %v", err)
	}

	logLevel, err := logrus.ParseLevel(appConfig.LogLevel)
	if err != nil {
		logrus.Error(err)
	} else {
		logrus.SetLevel(logLevel)
	}

	initMetricServer(appConfig.Metric)

	translator, err := newOpenAITranslator(appConfig.Translate)
	if err != nil {
		logrus.Fatal(err)
	}

	bot, err := newBot(appConfig.Bot, translator)
	if err != nil {
		logrus.Fatal(err)
	}

	bot.ServeBot()
}
