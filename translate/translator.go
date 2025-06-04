package translate

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/4O4-Not-F0und/Gura-Bot/selector"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"golang.org/x/time/rate"
)

type TranslatorOptions struct {
	Instance TranslatorInstance
	Timeout  int64

	// Failover
	FailoverConfig  FailoverConfig
	RateLimitConfig RateLimitConfig

	// Metrics
	UpMetric         *prometheus.GaugeVec
	SelectionMetric  *prometheus.CounterVec
	TasksMetric      *prometheus.GaugeVec
	TokensUsedMetric *prometheus.CounterVec
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

type baseTranslator struct {
	instance TranslatorInstance
	logger   *logrus.Entry
	limiter  *rate.Limiter
	timeout  time.Duration

	// Metrics
	upMetric         *prometheus.GaugeVec
	selectionMetric  *prometheus.CounterVec
	tasksMetric      *prometheus.GaugeVec
	tokensUsedMetric *prometheus.CounterVec

	// Failover
	failoverConfig            FailoverConfig
	failures                  int
	currentCooldownMultiplier int
	disableCycleCount         int
	disableUntil              time.Time
	isPermanentlyDisabled     bool
	failoverMu                *sync.Mutex
}

func newBaseTranslator(opts TranslatorOptions) (bt *baseTranslator) {
	bt = &baseTranslator{
		instance:              opts.Instance,
		timeout:               time.Duration(opts.Timeout) * time.Second,
		failoverConfig:        opts.FailoverConfig,
		upMetric:              opts.UpMetric,
		selectionMetric:       opts.SelectionMetric,
		tasksMetric:           opts.TasksMetric,
		tokensUsedMetric:      opts.TokensUsedMetric,
		failoverMu:            new(sync.Mutex),
		isPermanentlyDisabled: false,
	}
	// Initialize metrics
	bt.upMetric.WithLabelValues(bt.GetName()).Set(1)
	bt.selectionMetric.WithLabelValues(bt.GetName()).Add(0.0)
	for _, state := range allTranslationTaskStates {
		bt.tasksMetric.WithLabelValues(state, bt.GetName()).Add(0.0)
	}
	for _, t := range allTranslationTokenUsedTypes {
		bt.tokensUsedMetric.WithLabelValues(t, bt.GetName()).Add(0.0)
	}

	bt.logger = logrus.WithField("translator_name", bt.GetName())
	bt.resetFailoverState()

	// Initialize rate limiter
	if opts.RateLimitConfig.Enabled {
		bt.limiter = rate.NewLimiter(
			rate.Limit(opts.RateLimitConfig.RefillTPS),
			opts.RateLimitConfig.BucketSize,
		)
		bt.logger.Debugf(
			"rate limiter refill: %.2f tokens/s, bucket size: %d",
			opts.RateLimitConfig.RefillTPS,
			opts.RateLimitConfig.BucketSize,
		)
	}

	return
}

func (bt *baseTranslator) wait(ctx context.Context) (err error) {
	if bt.limiter != nil {
		err = bt.limiter.Wait(ctx)
	}
	return
}

func (bt *baseTranslator) Translate(req TranslateRequest) (tr *TranslateResponse, err error) {
	bt.selectionMetric.WithLabelValues(bt.GetName()).Inc()

	ctx, cancel := context.WithTimeout(context.Background(), bt.timeout)
	defer cancel()

	logger := bt.logger.WithField("trace_id", req.TraceId)

	logger.Trace("wating for limiter")
	bt.tasksMetric.WithLabelValues(translationStatePending, bt.GetName()).Inc()
	err = bt.wait(ctx)
	bt.tasksMetric.WithLabelValues(translationStatePending, bt.GetName()).Dec()
	if err != nil {
		return nil, fmt.Errorf("rate limiter wait failed: %w", err)
	}
	logger.Trace("acquired limiter")

	bt.tasksMetric.WithLabelValues(translationStateProcessing, bt.GetName()).Inc()
	defer bt.tasksMetric.WithLabelValues(translationStateProcessing, bt.GetName()).Dec()

	logger.Debug("wating for translate response")
	tr, err = bt.instance.Translate(ctx, req)
	if tr != nil {
		bt.tokensUsedMetric.WithLabelValues(
			translationTokenUsedTypeCompletion, bt.GetName()).Add(
			float64(tr.TokenUsage.Completion))
		bt.tokensUsedMetric.WithLabelValues(
			translationTokenUsedTypePrompt, bt.GetName()).Add(
			float64(tr.TokenUsage.Prompt))
	}

	if err != nil {
		bt.OnFailure()
		return
	}
	bt.OnSuccess()
	return
}

func (bt *baseTranslator) GetName() string {
	return bt.instance.Name()
}

func (bt *baseTranslator) OnSuccess() {
	bt.tasksMetric.WithLabelValues(translationStateSuccess, bt.GetName()).Inc()
	bt.upMetric.WithLabelValues(bt.GetName()).Set(1)

	bt.failoverMu.Lock()
	rst := bt.failures > 0 || bt.currentCooldownMultiplier > 0 || bt.disableCycleCount > 0
	bt.failoverMu.Unlock()
	if rst {
		bt.resetFailoverState()
	}
}

func (bt *baseTranslator) resetFailoverState() {
	bt.failoverMu.Lock()
	bt.failures = 0
	bt.currentCooldownMultiplier = 0
	bt.disableCycleCount = 0
	bt.isPermanentlyDisabled = false
	bt.failoverMu.Unlock()
	bt.logger.Debug("failover state reset")
}

func (bt *baseTranslator) OnFailure() {
	bt.logger.Warnf("New failure. Current failures: %d/%d", bt.failures, bt.failoverConfig.MaxFailures)
	bt.tasksMetric.WithLabelValues(translationStateFailed, bt.GetName()).Inc()
	bt.failoverMu.Lock()
	defer bt.failoverMu.Unlock()

	bt.failures += 1
	if bt.failures >= bt.failoverConfig.MaxFailures {
		bt.upMetric.WithLabelValues(bt.GetName()).Set(0)
		bt.failures = 0
		bt.currentCooldownMultiplier += 1
		bt.disableCycleCount += 1
		if bt.disableCycleCount >= bt.failoverConfig.MaxDisableCycles {
			bt.logger.Errorf("Reached maximum disable cycles: %d. Translator permanently disabled",
				bt.failoverConfig.MaxDisableCycles)
			bt.isPermanentlyDisabled = true
			return
		}
		bt.disableUntil = time.Now().Add(
			time.Duration(
				bt.currentCooldownMultiplier*
					bt.failoverConfig.CooldownBaseSec,
			) * time.Second)
		bt.logger.Warnf("reached maximum failures, disable it until %s",
			bt.disableUntil.Local().Format(time.RFC3339Nano))
	}
}

func (bt *baseTranslator) IsDisabled() bool {
	bt.failoverMu.Lock()
	ret := bt.isPermanentlyDisabled || time.Now().Before(bt.disableUntil)
	bt.failoverMu.Unlock()
	return ret
}

type weightTranslatorOptions struct {
	TranslatorOptions
	Weight int
}

type weightedTranslator struct {
	baseTranslator
	configWeight  int
	currentWeight int
	mu            *sync.Mutex
}

func (wt *weightedTranslator) GetConfigWeight() int {
	wt.mu.Lock()
	defer wt.mu.Unlock()
	return wt.configWeight
}

func (wt *weightedTranslator) GetCurrentWeight() int {
	wt.mu.Lock()
	defer wt.mu.Unlock()
	return wt.currentWeight
}

func (wt *weightedTranslator) SetCurrentWeight(s int) {
	wt.mu.Lock()
	wt.currentWeight = s
	wt.mu.Unlock()
}

func newWeightedTranslator(opts weightTranslatorOptions) (wt *weightedTranslator) {
	wt = &weightedTranslator{
		baseTranslator: *newBaseTranslator(opts.TranslatorOptions),
		configWeight:   opts.Weight,
		currentWeight:  0,
		mu:             &sync.Mutex{},
	}
	return
}
