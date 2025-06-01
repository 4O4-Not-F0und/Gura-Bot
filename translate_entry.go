package main

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/sirupsen/logrus"
)

type TranslatorEntry interface {
	Translator() (TranslatorInstance, error)
	TotalWeight() int
	AddInstance(TranslatorInstance, int)
}

type weightedTranslator struct {
	instance      TranslatorInstance
	configWeight  int
	currentWeight int
}

type weightedTranslatorEntry struct {
	s           []*weightedTranslator
	totalWeight int
	mu          *sync.Mutex
}

func newWeightedTranslatorEntry() (wte *weightedTranslatorEntry) {
	wte = &weightedTranslatorEntry{
		s:           make([]*weightedTranslator, 0),
		totalWeight: 0,
		mu:          &sync.Mutex{},
	}
	return
}

func (wte *weightedTranslatorEntry) AddInstance(instance TranslatorInstance, weight int) {
	wte.s = append(wte.s, &weightedTranslator{
		instance:      instance,
		configWeight:  weight,
		currentWeight: 0,
	})
	wte.totalWeight += weight
	logrus.Infof("added WRR translator '%s', weight: %d", instance.Name(), weight)
}

func (wte *weightedTranslatorEntry) TotalWeight() int {
	return wte.totalWeight
}

func (wte *weightedTranslatorEntry) Translator() (translator TranslatorInstance, err error) {
	if len(wte.s) == 0 {
		err = fmt.Errorf("no wrr translator available")
		return
	}
	if len(wte.s) == 1 {
		translator = wte.s[0].instance
		return
	}

	// Nginx's smooth weighted round-robin algorithm:
	// 1. For each server i: current_weight[i] = current_weight[i] + effective_weight[i]
	// 2. selected_server = server with highest current_weight
	// 3. current_weight[selected_server] = current_weight[selected_server] - total_weight
	selectedIndex := -1
	maxCurrentWeight := 0

	logrus.Trace("attempting to acquire wrr lock")
	wte.mu.Lock()
	logrus.Trace("acquired wrr lock")
	wrrBefore := wte.unsafeString()
	for i, entry := range wte.s {
		entry.currentWeight += entry.configWeight
		if selectedIndex == -1 || entry.currentWeight > maxCurrentWeight {
			maxCurrentWeight = entry.currentWeight
			selectedIndex = i
		}
	}

	if selectedIndex == -1 {
		wte.mu.Unlock()
		// Just for safe, should not happen if there are entries and totalWeight > 0
		return nil, fmt.Errorf("failed to select a translator using WRR")
	}
	wte.s[selectedIndex].currentWeight -= wte.totalWeight
	translator = wte.s[selectedIndex].instance
	wrrAfter := wte.unsafeString()
	wte.mu.Unlock()
	logrus.Trace("released wrr lock")

	logrus.Debugf("wrr before: %s", wrrBefore)
	logrus.Debugf("wrr after: %s", wrrAfter)
	logrus.Debugf("selected translator: %s", translator.Name())
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
