package main

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

const (
	namespace = "telegram_translate_bot"
)

type MetricConfig struct {
	Listen string `yaml:"listen"`
}

var (
	// States: "pending" (in bot's worker queue), "processing" (actively handled),
	//         "unauthorized" (terminal state for disallowed messages),
	//         "failed" (terminal state for error occurred while handling messages),
	//         "processed" (terminal state for successfully handled messages).
	metricMessages = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "messages",
			Help:      "Current number of messages being processed by the bot.",
		},
		[]string{"state", "chat_type"},
	)

	// States: "pending" (waiting for rate limiter),
	//         "processing" (waiting for translation API response),
	//         "success" (translation and parsing successful),
	//         "failed" (any step in translation failed).
	metricTranslationTasks = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "translation_tasks_total",
			Help:      "Total number of translation tasks, by state.",
		},
		[]string{"state", "chat_type"},
	)

	// Types: "completion" (output tokens)
	// 		  "prompt" (input tokens)
	metricTranslationTokensUsed = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "translation_tokens_used",
			Help:      "Used tokens of translation tasks.",
		},
		[]string{"type", "chat_type"},
	)
)

func initMetricServer(conf MetricConfig) {
	go func() {
		http.Handle("/metrics", promhttp.Handler())
		logrus.Infof("Metrics server listening on %s", conf.Listen)
		if err := http.ListenAndServe(conf.Listen, nil); err != nil {
			logrus.Fatalf("Failed to start metrics server: %v", err)
		}
	}()
}
