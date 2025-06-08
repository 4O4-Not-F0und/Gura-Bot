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
		RateLimitConfig:  conf.RateLimitConfig,
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

	OnSuccess()
	OnFailure()
	IsDisabled() bool
	// ResetFailoverState()
	Translate(TranslateRequest) (*TranslateResponse, error)
	GetName() string
}

type CommonTranslator struct {
	instance Instance
	logger   *logrus.Entry
	limiter  *rate.Limiter
	timeout  time.Duration

	// Metrics
	upMetric         *prometheus.GaugeVec
	selectionMetric  *prometheus.CounterVec
	tasksMetric      *prometheus.GaugeVec
	tokensUsedMetric *prometheus.CounterVec

	// Failover
	failoverConfig            common.FailoverConfig
	failures                  int
	currentCooldownMultiplier int
	disableCycleCount         int
	disableUntil              time.Time
	isPermanentlyDisabled     bool
	failoverMu                *sync.Mutex

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

		// Failover
		failoverConfig:        opts.FailoverConfig,
		failoverMu:            new(sync.Mutex),
		isPermanentlyDisabled: false,

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
	ct.resetFailoverState()

	// Initialize rate limiter
	if opts.RateLimitConfig.Enabled {
		ct.limiter = rate.NewLimiter(
			rate.Limit(opts.RateLimitConfig.RefillTPS),
			opts.RateLimitConfig.BucketSize,
		)
		ct.logger.Debugf(
			"rate limiter refill: %.2f tokens/s, bucket size: %d",
			opts.RateLimitConfig.RefillTPS,
			opts.RateLimitConfig.BucketSize,
		)
	}

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
		ct.OnFailure()
		return
	}
	ct.OnSuccess()
	return
}

func (ct *CommonTranslator) GetName() string {
	return ct.instance.Name()
}

func (ct *CommonTranslator) OnSuccess() {
	ct.tasksMetric.WithLabelValues(translationStateSuccess, ct.GetName()).Inc()
	ct.upMetric.WithLabelValues(ct.GetName()).Set(1)

	ct.failoverMu.Lock()
	rst := ct.failures > 0 || ct.currentCooldownMultiplier > 0 || ct.disableCycleCount > 0
	ct.failoverMu.Unlock()
	if rst {
		ct.resetFailoverState()
	}
}

func (ct *CommonTranslator) resetFailoverState() {
	ct.failoverMu.Lock()
	ct.failures = 0
	ct.currentCooldownMultiplier = 0
	ct.disableCycleCount = 0
	ct.isPermanentlyDisabled = false
	ct.failoverMu.Unlock()
	ct.logger.Debug("failover state reset")
}

func (ct *CommonTranslator) OnFailure() {
	ct.logger.Warnf("New failure. Current failures: %d/%d", ct.failures, ct.failoverConfig.MaxFailures)
	ct.tasksMetric.WithLabelValues(translationStateFailed, ct.GetName()).Inc()
	ct.failoverMu.Lock()
	defer ct.failoverMu.Unlock()

	ct.failures += 1
	if ct.failures >= ct.failoverConfig.MaxFailures {
		ct.upMetric.WithLabelValues(ct.GetName()).Set(0)
		ct.failures = 0
		ct.currentCooldownMultiplier += 1
		ct.disableCycleCount += 1
		if ct.disableCycleCount >= ct.failoverConfig.MaxDisableCycles {
			ct.logger.Errorf("Reached maximum disable cycles: %d. Translator permanently disabled",
				ct.failoverConfig.MaxDisableCycles)
			ct.isPermanentlyDisabled = true
			return
		}
		ct.disableUntil = time.Now().Add(
			time.Duration(
				ct.currentCooldownMultiplier*
					ct.failoverConfig.CooldownBaseSec,
			) * time.Second)
		ct.logger.Warnf("reached maximum failures, disable it until %s",
			ct.disableUntil.Local().Format(time.RFC3339Nano))
	}
}

func (ct *CommonTranslator) IsDisabled() bool {
	ct.failoverMu.Lock()
	ret := ct.isPermanentlyDisabled || time.Now().Before(ct.disableUntil)
	ct.failoverMu.Unlock()
	return ret
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
