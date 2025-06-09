package detector

import (
	"context"
	"fmt"

	"github.com/pemistahl/lingua-go"
	"github.com/sirupsen/logrus"
)

const (
	LINGUA = "lingua"
)

func init() {
	registerDetectorInstance(LINGUA, newLinguaInstance)
}

type InstanceLingua struct {
	baseInstance
	detector lingua.LanguageDetector
}

func newLinguaInstance(conf DetectorConfig) (instance Instance, err error) {
	ld := &InstanceLingua{
		baseInstance: baseInstance{
			name:                conf.Name,
			confidenceThreshold: conf.SourceLangConfidenceThreshold,
			sourceLangs:         conf.SourceLangFilter,
			logger:              logrus.WithField("detector_instance", conf.Name),
		},
		detector: nil,
	}

	allLanguages := map[string]lingua.Language{}
	availableLangs := []lingua.Language{}
	for _, l := range lingua.AllLanguages() {
		allLanguages[l.IsoCode639_1().String()] = l
	}

	for _, code := range conf.DetectLangs {
		if l, ok := allLanguages[code]; ok {
			ld.logger.Infof("found detect language: %s", code)
			availableLangs = append(availableLangs, l)
		} else {
			err = fmt.Errorf("unsupported language: %s", code)
			return
		}
	}

	ld.detector = lingua.NewLanguageDetectorBuilder().FromLanguages(availableLangs...).Build()
	return ld, nil
}

func (ld *InstanceLingua) Detect(_ context.Context, req DetectRequest) (resp *DetectResponse, err error) {
	lang := ""
	confidence := 0.0
	for _, cv := range ld.detector.ComputeLanguageConfidenceValues(req.Text) {
		l := cv.Language().IsoCode639_1().String()
		c := cv.Value()
		if c > confidence {
			lang = l
			confidence = c
		}
	}

	err = ld.checkDetectResult(lang, confidence)
	if err != nil {
		return
	}

	return &DetectResponse{
		Language:   lang,
		Confidence: confidence,
	}, nil
}
