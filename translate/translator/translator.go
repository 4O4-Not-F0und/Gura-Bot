package translator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/4O4-Not-F0und/Gura-Bot/metrics"
	"github.com/4O4-Not-F0und/Gura-Bot/selector"
	"github.com/4O4-Not-F0und/Gura-Bot/translate/common"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"golang.org/x/time/rate"
)

const (
	translationStatePending    = "pending"
	translationStateProcessing = "processing"
	translationStateSuccess    = "success"
	translationStateFailed     = "failed"

	translationTokenUsedTypeCompletion = "completion"
	translationTokenUsedTypePrompt     = "prompt"
)

var (
	allTranslationTaskStates = []string{
		translationStatePending,
		translationStateProcessing,
		translationStateSuccess,
		translationStateFailed,
	}

	allTranslationTokenUsedTypes = []string{
		translationTokenUsedTypeCompletion,
		translationTokenUsedTypePrompt,
	}

	registeredTranslatorInstances = map[string]newTranslatorInstanceFunc{}
)

type newTranslatorInstanceFunc func(TranslatorConfig) (Instance, error)

func registerTranslatorInstance(name string, f newTranslatorInstanceFunc) {
	if _, ok := registeredTranslatorInstances[name]; !ok {
		registeredTranslatorInstances[name] = f
		return
	}
	panic(fmt.Sprintf("translator instance type '%s' already registered", name))
}

func NewInstance(conf TranslatorConfig) (Instance, error) {
	if f, ok := registeredTranslatorInstances[conf.Type]; ok {
		return f(conf)
	}
	return nil, fmt.Errorf("unknown translator type: %s", conf.Type)
}

func NewTranslator(selectorType string, conf TranslatorConfig) (Translator, error) {
	instance, err := NewInstance(conf)
	if err != nil {
		return nil, err
	}

	opts := TranslatorOptions{
		Instance:         instance,
		Timeout:          conf.Timeout,
		UpMetric:         metrics.MetricTranslatorUp,
		SelectionMetric:  metrics.MetricTranslatorSelectionTotal,
		TasksMetric:      metrics.MetricTranslatorTasks,
		TokensUsedMetric: metrics.MetricTranslatorTokensUsed,
		FailoverConfig:   conf.Failover,
		RateLimitConfig:  conf.RateLimit,
		Weight:           conf.Weight,
	}

	switch selectorType {
	case selector.WRR, selector.FALLBACK:
		return NewCommonTranslator(opts), nil
	}
	return nil, fmt.Errorf("unrecognized translator selector: %s", selectorType)
}

type TranslateRequest struct {
	Text    string
	TraceId string
}

type TranslateResponse struct {
	Text       string
	TokenUsage struct {
		Completion int64
		Prompt     int64
	}
}

type TranslatorOptions struct {
	Instance Instance
	Timeout  int64

	// Failover
	FailoverConfig  common.FailoverConfig
	RateLimitConfig common.RateLimitConfig

	// Metrics
	UpMetric         *prometheus.GaugeVec
	SelectionMetric  *prometheus.CounterVec
	TasksMetric      *prometheus.GaugeVec
	TokensUsedMetric *prometheus.CounterVec

	// WRR
	Weight int
}

type Translator interface {
	selector.WeightedItem

	Translate(TranslateRequest) (*TranslateResponse, error)
	GetName() string
}

type CommonTranslator struct {
	instance        Instance
	logger          *logrus.Entry
	limiter         *rate.Limiter
	timeout         time.Duration
	failoverHandler common.FailoverHandler

	// Metrics
	upMetric         *prometheus.GaugeVec
	selectionMetric  *prometheus.CounterVec
	tasksMetric      *prometheus.GaugeVec
	tokensUsedMetric *prometheus.CounterVec

	// Weighted
	configWeight  int
	currentWeight int
	weightedMu    *sync.Mutex
}

func NewCommonTranslator(opts TranslatorOptions) (ct *CommonTranslator) {
	ct = &CommonTranslator{
		instance: opts.Instance,
		timeout:  time.Duration(opts.Timeout) * time.Second,

		upMetric:         opts.UpMetric,
		selectionMetric:  opts.SelectionMetric,
		tasksMetric:      opts.TasksMetric,
		tokensUsedMetric: opts.TokensUsedMetric,

		// Weighted
		configWeight:  opts.Weight,
		currentWeight: 0,
		weightedMu:    &sync.Mutex{},
	}
	// Initialize metrics
	ct.upMetric.WithLabelValues(ct.GetName()).Set(1)
	ct.selectionMetric.WithLabelValues(ct.GetName()).Add(0.0)
	for _, state := range allTranslationTaskStates {
		ct.tasksMetric.WithLabelValues(state, ct.GetName()).Add(0.0)
	}
	for _, t := range allTranslationTokenUsedTypes {
		ct.tokensUsedMetric.WithLabelValues(t, ct.GetName()).Add(0.0)
	}

	ct.logger = logrus.WithField("translator_name", ct.GetName())
	ct.failoverHandler = common.NewGeneralFailoverHandler(opts.FailoverConfig, ct.logger)
	ct.limiter = opts.RateLimitConfig.NewLimiterFromConfig(ct.logger)
	return
}

func (ct *CommonTranslator) wait(ctx context.Context) (err error) {
	if ct.limiter != nil {
		err = ct.limiter.Wait(ctx)
	}
	return
}

func (ct *CommonTranslator) Translate(req TranslateRequest) (tr *TranslateResponse, err error) {
	ct.selectionMetric.WithLabelValues(ct.GetName()).Inc()

	ctx, cancel := context.WithTimeout(context.Background(), ct.timeout)
	defer cancel()

	logger := ct.logger.WithField("trace_id", req.TraceId)

	logger.Trace("wating for limiter")
	ct.tasksMetric.WithLabelValues(translationStatePending, ct.GetName()).Inc()
	err = ct.wait(ctx)
	ct.tasksMetric.WithLabelValues(translationStatePending, ct.GetName()).Dec()
	if err != nil {
		return nil, fmt.Errorf("rate limiter wait failed: %w", err)
	}
	logger.Trace("acquired limiter")

	ct.tasksMetric.WithLabelValues(translationStateProcessing, ct.GetName()).Inc()
	defer ct.tasksMetric.WithLabelValues(translationStateProcessing, ct.GetName()).Dec()

	logger.Debug("wating for translate response")
	tr, err = ct.instance.Translate(ctx, req)
	if tr != nil {
		ct.tokensUsedMetric.WithLabelValues(
			translationTokenUsedTypeCompletion, ct.GetName()).Add(
			float64(tr.TokenUsage.Completion))
		ct.tokensUsedMetric.WithLabelValues(
			translationTokenUsedTypePrompt, ct.GetName()).Add(
			float64(tr.TokenUsage.Prompt))
	}

	if err != nil {
		ct.onFailure()
		return
	}
	ct.onSuccess()
	return
}

func (ct *CommonTranslator) GetName() string {
	return ct.instance.Name()
}

func (ct *CommonTranslator) onSuccess() {
	ct.tasksMetric.WithLabelValues(translationStateSuccess, ct.GetName()).Inc()
	ct.upMetric.WithLabelValues(ct.GetName()).Set(1)
	ct.failoverHandler.OnSuccess()
}

func (ct *CommonTranslator) onFailure() {
	ct.tasksMetric.WithLabelValues(translationStateFailed, ct.GetName()).Inc()
	if ct.failoverHandler.OnFailure() {
		ct.upMetric.WithLabelValues(ct.GetName()).Set(0)
	}
}

func (ct *CommonTranslator) IsDisabled() bool {
	return ct.failoverHandler.IsDisabled()
}

func (ct *CommonTranslator) GetConfigWeight() int {
	ct.weightedMu.Lock()
	defer ct.weightedMu.Unlock()
	return ct.configWeight
}

func (ct *CommonTranslator) GetCurrentWeight() int {
	ct.weightedMu.Lock()
	defer ct.weightedMu.Unlock()
	return ct.currentWeight
}

func (ct *CommonTranslator) SetCurrentWeight(s int) {
	ct.weightedMu.Lock()
	ct.currentWeight = s
	ct.weightedMu.Unlock()
}
