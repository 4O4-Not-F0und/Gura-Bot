package selector

const (
	WRR      = "wrr"
	FALLBACK = "fallback"
)

type Item interface {
	// IsDisabled checks if the item is currently disabled.
	IsDisabled() bool
	// GetName returns the name of the item (for logging/debugging).
	GetName() string
}

type Selector[T Item] interface {
	AddItem(T)
	Select() (T, error)
	TotalConfigWeight() int
}
