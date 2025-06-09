package detector

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
	detectionStatePending    = "pending"
	detectionStateProcessing = "processing"
	detectionStateSuccess    = "success"
	detectionStateFailed     = "failed"
)

var (
	registeredDetectorInstances = map[string]newDetectorInstanceFunc{}
	allDetectionTaskStates      = []string{
		detectionStatePending,
		detectionStateProcessing,
		detectionStateSuccess,
		detectionStateFailed,
	}
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
		UpMetric:        metrics.MetricDetectorUp,
		SelectionMetric: metrics.MetricDetectorSelectionTotal,
		TasksMetric:     metrics.MetricDetectorTasks,
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

	UpMetric        *prometheus.GaugeVec
	SelectionMetric *prometheus.CounterVec
	TasksMetric     *prometheus.GaugeVec

	// WRR
	Weight int
}

type GeneralLanguageDetector struct {
	instance        Instance
	logger          *logrus.Entry
	limiter         *rate.Limiter
	timeout         time.Duration
	failoverHandler common.FailoverHandler

	// Metrics
	upMetric        *prometheus.GaugeVec
	selectionMetric *prometheus.CounterVec
	tasksMetric     *prometheus.GaugeVec

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

		// Metrics
		upMetric:        opts.UpMetric,
		selectionMetric: opts.SelectionMetric,
		tasksMetric:     opts.TasksMetric,

		// Weighted
		configWeight:  opts.Weight,
		currentWeight: 0,
		weightedMu:    new(sync.Mutex),
	}
	// Initialize metrics
	gld.upMetric.WithLabelValues(gld.GetName()).Set(1)
	gld.selectionMetric.WithLabelValues(gld.GetName()).Add(0.0)
	for _, state := range allDetectionTaskStates {
		gld.tasksMetric.WithLabelValues(state, gld.GetName()).Add(0.0)
	}

	gld.failoverHandler = common.NewGeneralFailoverHandler(opts.FailoverConfig, gld.logger)
	gld.limiter = opts.RateLimitConfig.NewLimiterFromConfig(gld.logger)
	return
}

func (gld *GeneralLanguageDetector) Detect(req DetectRequest) (resp *DetectResponse, err error) {
	gld.selectionMetric.WithLabelValues(gld.GetName()).Inc()

	ctx, cancel := context.WithTimeout(context.Background(), gld.timeout)
	defer cancel()

	logger := gld.logger.WithField("trace_id", req.TraceId)

	logger.Trace("wating for limiter")
	gld.tasksMetric.WithLabelValues(detectionStatePending, gld.GetName()).Inc()
	err = gld.wait(ctx)
	gld.tasksMetric.WithLabelValues(detectionStatePending, gld.GetName()).Dec()
	if err != nil {
		return nil, fmt.Errorf("rate limiter wait failed: %w", err)
	}
	logger.Trace("acquired limiter")

	gld.tasksMetric.WithLabelValues(detectionStateProcessing, gld.GetName()).Inc()
	defer gld.tasksMetric.WithLabelValues(detectionStateProcessing, gld.GetName()).Dec()

	logger.Debug("wating for detect response")
	resp, err = gld.instance.Detect(ctx, req)

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
	gld.tasksMetric.WithLabelValues(detectionStateSuccess, gld.GetName()).Inc()
	gld.upMetric.WithLabelValues(gld.GetName()).Set(1)
	gld.failoverHandler.OnSuccess()
}

func (gld *GeneralLanguageDetector) onFailure() {
	gld.tasksMetric.WithLabelValues(detectionStateFailed, gld.GetName()).Inc()
	if gld.failoverHandler.OnFailure() {
		gld.upMetric.WithLabelValues(gld.GetName()).Set(0)
	}
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
