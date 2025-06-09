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
		RateLimitConfig: conf.RateLimit,
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
	instance        Instance
	logger          *logrus.Entry
	limiter         *rate.Limiter
	timeout         time.Duration
	failoverHandler common.FailoverHandler

	// Weighted
	configWeight  int
	currentWeight int
	weightedMu    *sync.Mutex
}

func newGeneralLanguageDetector(opts DetectorOptions) (gld *GeneralLanguageDetector) {
	gld = &GeneralLanguageDetector{
		instance: opts.Instance,
		timeout:  time.Duration(opts.Timeout) * time.Second,
		logger:   logrus.WithField("detector_name", opts.Instance.Name()),

		// Weighted
		configWeight:  opts.Weight,
		currentWeight: 0,
		weightedMu:    new(sync.Mutex),
	}
	gld.failoverHandler = common.NewGeneralFailoverHandler(opts.FailoverConfig, gld.logger)
	gld.limiter = opts.RateLimitConfig.NewLimiterFromConfig(gld.logger)
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
		gld.onFailure()
		return
	}
	gld.onSuccess()
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

func (gld *GeneralLanguageDetector) onSuccess() {
	gld.failoverHandler.OnSuccess()
}

func (gld *GeneralLanguageDetector) onFailure() {
	gld.failoverHandler.OnFailure()
}

func (gld *GeneralLanguageDetector) IsDisabled() bool {
	return gld.failoverHandler.IsDisabled()
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
