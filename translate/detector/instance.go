package detector

import (
	"context"

	"github.com/sirupsen/logrus"
)

type Instance interface {
	Detect(context.Context, DetectRequest) (*DetectResponse, error)
	Name() string
}

type baseInstance struct {
	name                string
	confidenceThreshold float64
	sourceLangs         []string
	logger              *logrus.Entry
}

func (t *baseInstance) Name() string {
	return t.name
}
