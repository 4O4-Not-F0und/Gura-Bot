package main

import (
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/4O4-Not-F0und/Gura-Bot/metrics"
	"github.com/4O4-Not-F0und/Gura-Bot/translate"
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
		TimestampFormat:        time.RFC3339Nano,
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
	logrus.Infof("loaded config from '%s'", configFile)

	err = reloadLogConfig(appConfig.LogLevel)
	if err != nil {
		logrus.Errorf("error parsing new log level '%s': %v", appConfig.LogLevel, err)
	}

	metrics.InitMetricServer(appConfig.Metric)

	translateService, err := translate.NewTranslateService(appConfig.TranslateService)
	if err != nil {
		logrus.Fatal(err)
	}

	bot, err := newBot(appConfig.Bot, translateService)
	if err != nil {
		logrus.Fatal(err)
	}

	go bot.ServeBot()
	handleSignals(bot)
}

func reloadLogConfig(level string) (err error) {
	logLevel, err := logrus.ParseLevel(level)
	if err != nil {
		return
	}
	logrus.Infof("log level changed to: %s", level)
	logrus.SetLevel(logLevel)
	return
}

func handleSignals(bot *Bot) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGHUP)

	for sig := range sigChan {
		switch sig {
		case syscall.SIGHUP:
			logrus.Infof("received %s, attempting to reload config", sig.String())

			appConfig, err := loadConfig(configFile)
			if err != nil {
				logrus.Errorf("error reloading config: %v", err)
				continue
			}

			err = reloadLogConfig(appConfig.LogLevel)
			if err != nil {
				logrus.Errorf("error parsing new log level '%s': %v", appConfig.LogLevel, err)
				continue
			}

			translateService, err := translate.NewTranslateService(appConfig.TranslateService)
			if err != nil {
				logrus.Error(err)
				continue
			}

			err = bot.Reload(appConfig.Bot, translateService)
			if err != nil {
				logrus.Error(err)
				continue
			}

			logrus.Info("config reloaded")
		}
	}
}
