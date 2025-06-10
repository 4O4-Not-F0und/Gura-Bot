package detector

import (
	"context"
	"fmt"
	"slices"

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

func (t *baseInstance) checkDetectResult(lang string, confidence float64) (err error) {
	if lang == "" {
		err = newWeakError(fmt.Errorf("no reliable language detected"))
		return
	}
	if !slices.Contains(t.sourceLangs, lang) {
		err = newWeakError(fmt.Errorf("detected language '%s' is not in the configured source language filter", lang))
		return
	}
	if confidence < t.confidenceThreshold {
		err = newWeakError(
			fmt.Errorf("detected language '%s' (confidence: %.2f) is below threshold (%.2f)",
				lang, confidence, t.confidenceThreshold),
		)
		return
	}
	return
}
