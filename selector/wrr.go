package selector

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/sirupsen/logrus"
)

type Selector[T any] interface {
	AddItem(T)
	Select() (T, error)
	TotalConfigWeight() int
}

// WeightedItem defines the interface that items managed by the generic WRR selector must implement.
type WeightedItem interface {
	// GetConfigWeight returns the configured weight of the item.
	GetConfigWeight() int
	// GetCurrentWeight returns the current weight of the item (used by the WRR algorithm).
	GetCurrentWeight() int
	// SetCurrentWeight sets the current weight of the item.
	SetCurrentWeight(int)
	// IsDisabled checks if the item is currently disabled.
	IsDisabled() bool
	// GetName returns the name of the item (for logging/debugging).
	GetName() string
}

// WeightedRoundRobinSelector is a generic implementation of the Smooth Weighted Round Robin algorithm.
type WeightedRoundRobinSelector[T WeightedItem] struct {
	items             []T
	totalConfigWeight int
	mu                *sync.Mutex
}

// NewWeightedRoundRobinSelector creates a new generic WeightedRoundRobinSelector.
func NewWeightedRoundRobinSelector[T WeightedItem]() *WeightedRoundRobinSelector[T] {
	return &WeightedRoundRobinSelector[T]{
		items: make([]T, 0),
		mu:    &sync.Mutex{},
	}
}

// AddItem adds an item to the selector.
func (s *WeightedRoundRobinSelector[T]) AddItem(item T) {
	s.mu.Lock()
	s.items = append(s.items, item)
	s.totalConfigWeight += item.GetConfigWeight()
	logrus.Infof("added WRR item '%s', weight: %d", item.GetName(), item.GetConfigWeight())
	s.mu.Unlock()
}

// Select chooses an item based on the Smooth Weighted Round Robin algorithm.
// It returns the selected item or an error if no item is available or all are disabled.
func (s *WeightedRoundRobinSelector[T]) Select() (item T, err error) {
	logrus.Trace("attempting to acquire wrr lock")
	s.mu.Lock()
	logrus.Trace("acquired wrr lock")

	defer func() {
		s.mu.Unlock()
		logrus.Trace("released wrr lock")
	}()

	if len(s.items) == 0 {
		return item, fmt.Errorf("no items available in selector")
	}

	selectedIndex := -1
	maxCurrentWeight := 0
	wrrBefore := s.unsafeString()

	// Nginx's smooth weighted round-robin (sWRR) algorithm:
	for i := range s.items {
		// Use index to get a mutable copy if T is a struct
		entry := s.items[i]
		if entry.IsDisabled() {
			// Skip disabled item
			continue
		}

		// sWRR: 1. For each server i: current_weight[i] = current_weight[i] + effective_weight[i]
		entry.SetCurrentWeight(entry.GetCurrentWeight() + entry.GetConfigWeight())

		if selectedIndex == -1 || entry.GetCurrentWeight() > maxCurrentWeight {
			// sWRR: 2. selected_server = server with highest current_weight
			maxCurrentWeight = entry.GetCurrentWeight()
			selectedIndex = i
		}
	}

	if selectedIndex == -1 {
		return item, fmt.Errorf("no available item")
	}

	selectedItem := s.items[selectedIndex]
	// sWRR: 3. current_weight[selected_server] = current_weight[selected_server] - total_weight
	selectedItem.SetCurrentWeight(selectedItem.GetCurrentWeight() - s.totalConfigWeight)

	wrrAfter := s.unsafeString()
	logrus.Tracef("wrr before: %s", wrrBefore)
	logrus.Tracef("wrr after: %s", wrrAfter)

	// Update the item in the slice if T is a struct
	s.items[selectedIndex] = selectedItem

	logrus.Debugf("selected item: %s", selectedItem.GetName())
	return selectedItem, nil
}

// TotalConfigWeight returns the sum of configured weights of all items.
func (s *WeightedRoundRobinSelector[T]) TotalConfigWeight() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.totalConfigWeight
}

func (s *WeightedRoundRobinSelector[T]) unsafeString() string {
	m := map[string]int{}
	for _, item := range s.items {
		m[item.GetName()] = item.GetCurrentWeight()
	}
	b, _ := json.Marshal(m)
	return string(b)
}
