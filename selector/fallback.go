package selector

import (
	"fmt"
	"sync"

	"github.com/sirupsen/logrus"
)

// FallbackSelector implements a selector that prioritizes a primary item
// and falls back to a secondary item if the primary is disabled.
// It iterates through a list of items and selects the first one that is not disabled.
// It conforms to the Selector interface.
type FallbackSelector[T Item] struct {
	items []T
	mu    *sync.Mutex
}

// NewFallbackSelector creates a new FallbackSelector.
func NewFallbackSelector[T Item]() *FallbackSelector[T] {
	return &FallbackSelector[T]{
		items: make([]T, 0),
		mu:    &sync.Mutex{},
	}
}

// AddItem adds an item to the selector.
// Items will be tried in the order they are added.
func (s *FallbackSelector[T]) AddItem(item T) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.items = append(s.items, item)
	logrus.Infof("FallbackSelector: added item '%s'", item.GetName())
}

// Select chooses an item. It iterates through the configured items
// and returns the first item that is not disabled.
// It returns an error if no suitable item can be selected.
func (s *FallbackSelector[T]) Select() (item T, err error) {
	logrus.Trace("FallbackSelector: attempting to acquire lock")
	s.mu.Lock()
	logrus.Trace("FallbackSelector: acquired lock")
	defer func() {
		logrus.Trace("FallbackSelector: attempting to release lock")
		s.mu.Unlock()
		logrus.Trace("FallbackSelector: released lock")
	}()

	if len(s.items) == 0 {
		err = fmt.Errorf("fallback selector: no items configured")
		logrus.Debug(err)
		return
	}

	for _, currentItem := range s.items {
		if !currentItem.IsDisabled() {
			logrus.Debugf("FallbackSelector: selected item '%s'", currentItem.GetName())
			return currentItem, nil
		}
		logrus.Debugf("FallbackSelector: item '%s' is disabled, trying next", currentItem.GetName())
	}
	logrus.Warn("FallbackSelector: all configured items are disabled")
	err = fmt.Errorf("fallback selector: all configured items are disabled")
	return
}

// TotalConfigWeight returns 0 for FallbackSelector as weights are not applicable.
func (s *FallbackSelector[T]) TotalConfigWeight() int {
	return 0
}
