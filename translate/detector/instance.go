package detector

import "context"

type Instance interface {
	Detect(context.Context, DetectRequest) (*DetectResponse, error)
	Name() string
}
