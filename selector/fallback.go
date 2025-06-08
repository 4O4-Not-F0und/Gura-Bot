package selector

import (
	"fmt"
	"sync"

	"github.com/sirupsen/logrus"
)

const (
	FALLBACK = "fallback"
)

// FallbackSelector implements a selector that prioritizes a primary item
// and falls back to a secondary item if the primary is disabled.
// It iterates through a list of items and selects the first one that is not disabled.
// It conforms to the Selector interface.
type FallbackSelector[T Item] struct {
	items  []T
	mu     *sync.Mutex
	logger *logrus.Entry
}

// NewFallbackSelector creates a new FallbackSelector.
func NewFallbackSelector[T Item]() *FallbackSelector[T] {
	return &FallbackSelector[T]{
		items:  make([]T, 0),
		mu:     &sync.Mutex{},
		logger: logrus.WithField("selector", FALLBACK),
	}
}

// AddItem adds an item to the selector.
// Items will be tried in the order they are added.
func (s *FallbackSelector[T]) AddItem(item T) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.items = append(s.items, item)
	s.logger.Infof("added item '%s'", item.GetName())
}

// Select chooses an item. It iterates through the configured items
// and returns the first item that is not disabled.
// It returns an error if no suitable item can be selected.
func (s *FallbackSelector[T]) Select() (item T, err error) {
	s.logger.Trace("attempting to acquire lock")
	s.mu.Lock()
	s.logger.Trace("acquired lock")
	defer func() {
		s.logger.Trace("attempting to release lock")
		s.mu.Unlock()
		s.logger.Trace("released lock")
	}()

	if len(s.items) == 0 {
		err = fmt.Errorf("fallback selector: no items configured")
		s.logger.Debug(err)
		return
	}

	for _, currentItem := range s.items {
		if !currentItem.IsDisabled() {
			s.logger.Debugf("selected item '%s'", currentItem.GetName())
			return currentItem, nil
		}
		s.logger.Debugf("item '%s' is disabled, trying next", currentItem.GetName())
	}
	s.logger.Warn("all configured items are disabled")
	err = fmt.Errorf("fallback selector: all configured items are disabled")
	return
}

// TotalConfigWeight returns 0 for FallbackSelector as weights are not applicable.
func (s *FallbackSelector[T]) TotalConfigWeight() int {
	return 0
}

func (s *FallbackSelector[T]) GetType() string {
	return FALLBACK
}
