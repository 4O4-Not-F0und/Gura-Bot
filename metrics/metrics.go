package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

const (
	namespace = "gura_bot"
)

type MetricConfig struct {
	Listen string `yaml:"listen"`
}

var (
	// States: "pending" (in bot's worker queue), "processing" (actively handled),
	//         "unauthorized" (terminal state for disallowed messages),
	//         "failed" (terminal state for error occurred while handling messages),
	//         "processed" (terminal state for successfully handled messages).
	MetricMessages = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "messages_total",
			Help:      "Current number of messages being processed by the bot.",
		},
		[]string{"state", "chat_type"},
	)

	// States: "pending" (waiting for rate limiter),
	//         "processing" (waiting for translation API response),
	//         "success" (translation and parsing successful),
	//         "failed" (any step in translation failed).
	MetricTranslatorTasks = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "translator_tasks_total",
			Help:      "Total number of translation tasks, by state.",
		},
		[]string{"state", "translator_name"},
	)

	// Types: "completion" (output tokens)
	// 		  "prompt" (input tokens)
	MetricTranslatorTokensUsed = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "translator_tokens_used",
			Help:      "Used tokens of translation tasks.",
		},
		[]string{"token_type", "translator_name"},
	)

	// Gauge for translator up status
	// Value is 1 if the translator is up, 0 if it is disabled.
	MetricTranslatorUp = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "translator_up",
			Help:      "Indicates if a translator is currently up and operational. 1 for up, 0 for disabled.",
		},
		[]string{"translator_name"},
	)

	// Gauge for translator selected times
	MetricTranslatorSelectionTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "translator_selection_total",
			Help:      "Times of translator instance was chosen.",
		},
		[]string{"translator_name"},
	)
)

func InitMetricServer(conf MetricConfig) {
	go func() {
		http.Handle("/metrics", promhttp.Handler())
		logrus.Infof("Metrics server listening on %s", conf.Listen)
		if err := http.ListenAndServe(conf.Listen, nil); err != nil {
			logrus.Fatalf("Failed to start metrics server: %v", err)
		}
	}()
}
