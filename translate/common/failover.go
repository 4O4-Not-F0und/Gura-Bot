package common

import (
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

type FailoverHandler interface {
	OnSuccess()
	OnFailure() (isDisabled bool)
	IsDisabled() bool
}

type GeneralFailoverHandler struct {
	// Logger already has component context from initialization
	logger *logrus.Entry

	// Failover
	failoverConfig            FailoverConfig
	failures                  int
	currentCooldownMultiplier int
	disableCycleCount         int
	disableUntil              time.Time
	isPermanentlyDisabled     bool
	mu                        sync.Mutex
}

func NewGeneralFailoverHandler(conf FailoverConfig, logger *logrus.Entry) (s *GeneralFailoverHandler) {
	s = &GeneralFailoverHandler{
		logger:                logger,
		failoverConfig:        conf,
		mu:                    sync.Mutex{},
		isPermanentlyDisabled: false,
	}

	// It's safe here
	s.resetState()
	return
}

func (gfh *GeneralFailoverHandler) OnSuccess() {
	gfh.mu.Lock()
	rst := gfh.failures > 0 || gfh.currentCooldownMultiplier > 0 || gfh.disableCycleCount > 0
	if rst {
		gfh.resetState()
	}
	gfh.mu.Unlock()
}

// resetState resets all failover states.
// ATTENTION: NOT A THREAD SAFE OPERATION
func (gfh *GeneralFailoverHandler) resetState() {
	gfh.failures = 0
	gfh.currentCooldownMultiplier = 0
	gfh.disableCycleCount = 0
	gfh.isPermanentlyDisabled = false
	gfh.logger.Debug("failover state reset")
}

// OnFailure processes a failure, updates failover counters,
// and determines if the component should be temporarily disabled.
// Returns true if the component has just entered a disabled state
// (cooldown or permanent) due to this failure.
func (gfh *GeneralFailoverHandler) OnFailure() (isDisabled bool) {
	gfh.logger.Warnf("new failure. Current failures: %d/%d", gfh.failures, gfh.failoverConfig.MaxFailures)
	gfh.mu.Lock()
	defer gfh.mu.Unlock()

	gfh.failures += 1
	if gfh.failures >= gfh.failoverConfig.MaxFailures {
		gfh.failures = 0
		gfh.currentCooldownMultiplier += 1
		gfh.disableCycleCount += 1
		if gfh.disableCycleCount >= gfh.failoverConfig.MaxDisableCycles {
			gfh.logger.Errorf("reached maximum disable cycles: %d. Component permanently disabled",
				gfh.failoverConfig.MaxDisableCycles)
			gfh.isPermanentlyDisabled = true
			return true
		}
		gfh.disableUntil = time.Now().Add(
			time.Duration(
				gfh.currentCooldownMultiplier*
					gfh.failoverConfig.CooldownBaseSec,
			) * time.Second)
		gfh.logger.Warnf("reached maximum failures, disable it until %s",
			gfh.disableUntil.Local().Format(time.RFC3339Nano))
		return true
	}
	return
}

func (gfh *GeneralFailoverHandler) IsDisabled() bool {
	gfh.mu.Lock()
	ret := gfh.isPermanentlyDisabled || time.Now().Before(gfh.disableUntil)
	gfh.mu.Unlock()
	return ret
}
