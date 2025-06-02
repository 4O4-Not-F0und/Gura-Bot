package main

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
)

type Translator interface {
	OnSuccess()
	OnFailure()
	IsDisabled() bool
	// ResetFailoverState()
	Translate(string) (*TranslateResponse, error)
	InstanceName() string
}

type baseTranslator struct {
	instance        TranslatorInstance
	logger          *logrus.Entry
	upMetric        *prometheus.GaugeVec
	selectionMetric *prometheus.CounterVec

	// Failover
	failoverConfig            FailoverConfig
	failures                  int
	currentCooldownMultiplier int
	disableCycleCount         int
	disableUntil              time.Time
	isPermanentlyDisabled     bool
	failoverMu                *sync.Mutex
}

func newBaseTranslator(opts TranslatorEntryInstanceOptions) (bt *baseTranslator) {
	bt = &baseTranslator{
		instance:              opts.Instance,
		failoverConfig:        opts.FailoverConfig,
		upMetric:              opts.UpMetric,
		selectionMetric:       opts.SelectionMetric,
		failoverMu:            new(sync.Mutex),
		isPermanentlyDisabled: false,
	}
	bt.upMetric.WithLabelValues(bt.InstanceName()).Set(1)
	bt.selectionMetric.WithLabelValues(bt.InstanceName()).Add(0.0)
	bt.logger = logrus.WithField("translator_name", bt.InstanceName())
	bt.resetFailoverState()
	return
}

func (bt *baseTranslator) Translate(s string) (tr *TranslateResponse, err error) {
	bt.selectionMetric.WithLabelValues(bt.InstanceName()).Inc()
	tr, err = bt.instance.Translate(s)
	if err != nil {
		bt.OnFailure()
		return
	}
	bt.OnSuccess()
	return
}

func (bt *baseTranslator) InstanceName() string {
	return bt.instance.Name()
}

func (bt *baseTranslator) OnSuccess() {
	bt.failoverMu.Lock()
	rst := bt.failures > 0 || bt.currentCooldownMultiplier > 0 || bt.disableCycleCount > 0
	bt.failoverMu.Unlock()

	if rst {
		bt.resetFailoverState()
		bt.upMetric.WithLabelValues(bt.InstanceName()).Set(1)
	}
}

func (bt *baseTranslator) resetFailoverState() {
	bt.failoverMu.Lock()
	bt.failures = 0
	bt.currentCooldownMultiplier = 0
	bt.disableCycleCount = 0
	bt.isPermanentlyDisabled = false
	bt.failoverMu.Unlock()
}

func (bt *baseTranslator) OnFailure() {
	bt.logger.Warnf("New failure. Current failures: %d/%d", bt.failures, bt.failoverConfig.MaxFailures)
	bt.failoverMu.Lock()
	defer bt.failoverMu.Unlock()

	bt.failures += 1
	if bt.failures >= bt.failoverConfig.MaxFailures {
		bt.upMetric.WithLabelValues(bt.InstanceName()).Set(0)
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

type weightedTranslator struct {
	baseTranslator
	configWeight  int
	currentWeight int
}

func newWeightedTranslator(opts TranslatorEntryInstanceOptions) (wt *weightedTranslator) {
	wt = &weightedTranslator{
		baseTranslator: *newBaseTranslator(opts),
		configWeight:   opts.Weight,
		currentWeight:  0,
	}
	return
}

// Translator Entries
type TranslatorEntry interface {
	Translator() (Translator, error)
	TotalConfigWeight() int
	AddInstance(TranslatorEntryInstanceOptions)
}

type TranslatorEntryInstanceOptions struct {
	Instance        TranslatorInstance
	FailoverConfig  FailoverConfig
	UpMetric        *prometheus.GaugeVec
	SelectionMetric *prometheus.CounterVec
	Weight          int
}

type weightedTranslatorEntry struct {
	s                 []*weightedTranslator
	totalConfigWeight int
	mu                *sync.Mutex
}

func newWeightedTranslatorEntry() (wte *weightedTranslatorEntry) {
	wte = &weightedTranslatorEntry{
		s:                 make([]*weightedTranslator, 0),
		totalConfigWeight: 0,
		mu:                &sync.Mutex{},
	}
	return
}

func (wte *weightedTranslatorEntry) AddInstance(opts TranslatorEntryInstanceOptions) {
	wte.s = append(wte.s, newWeightedTranslator(opts))
	wte.totalConfigWeight += opts.Weight
	logrus.Infof("added WRR translator '%s', weight: %d", opts.Instance.Name(), opts.Weight)
}

func (wte *weightedTranslatorEntry) TotalConfigWeight() int {
	return wte.totalConfigWeight
}

func (wte *weightedTranslatorEntry) Translator() (translator Translator, err error) {
	if len(wte.s) == 0 {
		err = fmt.Errorf("no wrr translator available")
		return
	}

	// Nginx's smooth weighted round-robin (sWRR) algorithm:
	selectedIndex := -1
	maxCurrentWeight := 0

	logrus.Trace("attempting to acquire wrr lock")
	wte.mu.Lock()
	logrus.Trace("acquired wrr lock")
	wrrBefore := wte.unsafeString()

	for i, entry := range wte.s {
		if entry.IsDisabled() {
			// Skip disabled translator
			continue
		}

		// sWRR: 1. For each server i: current_weight[i] = current_weight[i] + effective_weight[i]
		entry.currentWeight += entry.configWeight
		if selectedIndex == -1 || entry.currentWeight > maxCurrentWeight {
			// sWRR: 2. selected_server = server with highest current_weight
			maxCurrentWeight = entry.currentWeight
			selectedIndex = i
		}
	}

	if selectedIndex == -1 {
		wte.mu.Unlock()
		logrus.Trace("released wrr lock")
		return nil, fmt.Errorf("no available translator")
	}

	// sWRR: 3. current_weight[selected_server] = current_weight[selected_server] - total_weight
	wte.s[selectedIndex].currentWeight -= wte.totalConfigWeight
	translator = wte.s[selectedIndex]

	wrrAfter := wte.unsafeString()
	wte.mu.Unlock()
	logrus.Trace("released wrr lock")

	logrus.Debugf("wrr before: %s", wrrBefore)
	logrus.Debugf("wrr after: %s", wrrAfter)
	logrus.Debugf("selected translator: %s", translator.InstanceName())
	return
}

func (wte *weightedTranslatorEntry) unsafeString() string {
	m := map[string]int{}
	for _, entry := range wte.s {
		m[entry.instance.Name()] = entry.currentWeight
	}
	b, _ := json.Marshal(m)
	return string(b)
}
