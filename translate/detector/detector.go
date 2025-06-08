package detector

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/4O4-Not-F0und/Gura-Bot/selector"
	"github.com/4O4-Not-F0und/Gura-Bot/translate/common"
	"github.com/sirupsen/logrus"
	"golang.org/x/time/rate"
)

var (
	registeredDetectorInstances = map[string]newDetectorInstanceFunc{}
)

type newDetectorInstanceFunc func(DetectorConfig) (Instance, error)

func registerDetectorInstance(name string, f newDetectorInstanceFunc) {
	if _, ok := registeredDetectorInstances[name]; !ok {
		registeredDetectorInstances[name] = f
		return
	}
	panic(fmt.Sprintf("detector instance type '%s' already registered", name))
}

func NewDetectorInstance(conf DetectorConfig) (Instance, error) {
	if f, ok := registeredDetectorInstances[conf.Type]; ok {
		return f(conf)
	}
	return nil, fmt.Errorf("unknown detector type '%s', detector: %s", conf.Type, conf.Name)
}

func NewDetector(selectorType string, conf DetectorConfig) (LanguageDetector, error) {
	instance, err := NewDetectorInstance(conf)
	if err != nil {
		return nil, err
	}

	opts := DetectorOptions{
		Instance:        instance,
		Timeout:         conf.Timeout,
		FailoverConfig:  conf.Failover,
		RateLimitConfig: conf.RateLimitConfig,
		Weight:          conf.Weight,
	}

	switch selectorType {
	case selector.WRR, selector.FALLBACK:
		return newGeneralLanguageDetector(opts), nil
	}
	return nil, fmt.Errorf("unrecognized translator selector: %s", selectorType)
}

type DetectRequest struct {
	Text    string
	TraceId string
}

type DetectResponse struct {
	Language   string
	Confidence float64
}

type LanguageDetector interface {
	selector.WeightedItem

	OnSuccess()
	OnFailure()
	IsDisabled() bool
	Detect(DetectRequest) (*DetectResponse, error)
	GetName() string
}

type DetectorOptions struct {
	Instance Instance
	Timeout  int64

	// Failover
	FailoverConfig  common.FailoverConfig
	RateLimitConfig common.RateLimitConfig

	/* Metrics
	UpMetric         *prometheus.GaugeVec
	SelectionMetric  *prometheus.CounterVec
	TasksMetric      *prometheus.GaugeVec
	TokensUsedMetric *prometheus.CounterVec
	*/

	// WRR
	Weight int
}

type GeneralLanguageDetector struct {
	instance Instance
	logger   *logrus.Entry
	limiter  *rate.Limiter
	timeout  time.Duration

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

func newGeneralLanguageDetector(opts DetectorOptions) (gld *GeneralLanguageDetector) {
	gld = &GeneralLanguageDetector{
		instance:              opts.Instance,
		timeout:               time.Duration(opts.Timeout) * time.Second,
		failoverConfig:        opts.FailoverConfig,
		failoverMu:            new(sync.Mutex),
		isPermanentlyDisabled: false,
		logger:                logrus.WithField("detector_name", opts.Instance.Name()),

		// Weighted
		configWeight:  opts.Weight,
		currentWeight: 0,
		weightedMu:    new(sync.Mutex),
	}
	gld.resetFailoverState()

	// Initialize rate limiter
	if opts.RateLimitConfig.Enabled {
		gld.limiter = rate.NewLimiter(
			rate.Limit(opts.RateLimitConfig.RefillTPS),
			opts.RateLimitConfig.BucketSize,
		)
		gld.logger.Debugf(
			"rate limiter refill: %.2f tokens/s, bucket size: %d",
			opts.RateLimitConfig.RefillTPS,
			opts.RateLimitConfig.BucketSize,
		)
	}

	return
}

func (gld *GeneralLanguageDetector) Detect(req DetectRequest) (resp *DetectResponse, err error) {
	// gld.selectionMetric.WithLabelValues(gld.GetName()).Inc()

	ctx, cancel := context.WithTimeout(context.Background(), gld.timeout)
	defer cancel()

	logger := gld.logger.WithField("trace_id", req.TraceId)

	logger.Trace("wating for limiter")
	// gld.tasksMetric.WithLabelValues(translationStatePending, gld.GetName()).Inc()
	err = gld.wait(ctx)
	// gld.tasksMetric.WithLabelValues(translationStatePending, gld.GetName()).Dec()
	if err != nil {
		return nil, fmt.Errorf("rate limiter wait failed: %w", err)
	}
	logger.Trace("acquired limiter")

	// gld.tasksMetric.WithLabelValues(translationStateProcessing, gld.GetName()).Inc()
	// defer gld.tasksMetric.WithLabelValues(translationStateProcessing, gld.GetName()).Dec()

	logger.Debug("wating for detect response")
	resp, err = gld.instance.Detect(ctx, req)
	/*
		if tr != nil {
			gld.tokensUsedMetric.WithLabelValues(
				translationTokenUsedTypeCompletion, gld.GetName()).Add(
				float64(tr.TokenUsage.Completion))
			gld.tokensUsedMetric.WithLabelValues(
				translationTokenUsedTypePrompt, gld.GetName()).Add(
				float64(tr.TokenUsage.Prompt))
		}
	*/

	if err != nil {
		gld.OnFailure()
		return
	}
	gld.OnSuccess()
	return
}

func (gld *GeneralLanguageDetector) wait(ctx context.Context) (err error) {
	if gld.limiter != nil {
		err = gld.limiter.Wait(ctx)
	}
	return
}

func (gld *GeneralLanguageDetector) GetName() string {
	return gld.instance.Name()
}

func (gld *GeneralLanguageDetector) OnSuccess() {
	gld.failoverMu.Lock()
	rst := gld.failures > 0 || gld.currentCooldownMultiplier > 0 || gld.disableCycleCount > 0
	gld.failoverMu.Unlock()
	if rst {
		gld.resetFailoverState()
	}
}

func (gld *GeneralLanguageDetector) resetFailoverState() {
	gld.failoverMu.Lock()
	gld.failures = 0
	gld.currentCooldownMultiplier = 0
	gld.disableCycleCount = 0
	gld.isPermanentlyDisabled = false
	gld.failoverMu.Unlock()
	gld.logger.Debug("failover state reset")
}

func (gld *GeneralLanguageDetector) OnFailure() {
	gld.logger.Warnf("New failure. Current failures: %d/%d", gld.failures, gld.failoverConfig.MaxFailures)
	gld.failoverMu.Lock()
	defer gld.failoverMu.Unlock()

	gld.failures += 1
	if gld.failures >= gld.failoverConfig.MaxFailures {
		gld.failures = 0
		gld.currentCooldownMultiplier += 1
		gld.disableCycleCount += 1
		if gld.disableCycleCount >= gld.failoverConfig.MaxDisableCycles {
			gld.logger.Errorf("Reached maximum disable cycles: %d. Translator permanently disabled",
				gld.failoverConfig.MaxDisableCycles)
			gld.isPermanentlyDisabled = true
			return
		}
		gld.disableUntil = time.Now().Add(
			time.Duration(
				gld.currentCooldownMultiplier*
					gld.failoverConfig.CooldownBaseSec,
			) * time.Second)
		gld.logger.Warnf("reached maximum failures, disable it until %s",
			gld.disableUntil.Local().Format(time.RFC3339Nano))
	}
}

func (gld *GeneralLanguageDetector) IsDisabled() bool {
	gld.failoverMu.Lock()
	ret := gld.isPermanentlyDisabled || time.Now().Before(gld.disableUntil)
	gld.failoverMu.Unlock()
	return ret
}

func (gld *GeneralLanguageDetector) GetConfigWeight() int {
	gld.weightedMu.Lock()
	defer gld.weightedMu.Unlock()
	return gld.configWeight
}

func (gld *GeneralLanguageDetector) GetCurrentWeight() int {
	gld.weightedMu.Lock()
	defer gld.weightedMu.Unlock()
	return gld.currentWeight
}

func (gld *GeneralLanguageDetector) SetCurrentWeight(s int) {
	gld.weightedMu.Lock()
	gld.currentWeight = s
	gld.weightedMu.Unlock()
}
