package detector

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/4O4-Not-F0und/detectlanguage-go"
	"github.com/sirupsen/logrus"
)

const (
	DETECT_LANGUAGE = "detect_language"
)

func init() {
	registerDetectorInstance(DETECT_LANGUAGE, newDetectLanguageInstance)
}

type InstanceDetectLanguage struct {
	baseInstance
	client *detectlanguage.Client
}

func newDetectLanguageInstance(conf DetectorConfig) (instance Instance, err error) {
	ld := &InstanceDetectLanguage{
		baseInstance: baseInstance{
			name:                conf.Name,
			confidenceThreshold: conf.SourceLangConfidenceThreshold,
			sourceLangs:         conf.SourceLangFilter,
			logger:              logrus.WithField("detector_instance", conf.Name),
		},
		client: detectlanguage.New(conf.Token),
	}

	// Check API status
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ld.logger.Debug("checking detectlanguage api instance status")
	var user *detectlanguage.UserStatusResponse
	user, err = ld.client.UserStatus(ctx)
	if err != nil {
		err = fmt.Errorf("detectlanguage api status error: %w", err)
		return
	}
	if user.Status != "ACTIVE" {
		err = fmt.Errorf("detectlanguage api status error, user status: %s", user.Status)
		return
	}

	b, _ := json.Marshal(user)
	ld.logger.Info(string(b))

	return ld, nil
}

func (ld *InstanceDetectLanguage) Detect(ctx context.Context, req DetectRequest) (resp *DetectResponse, err error) {
	var r []*detectlanguage.DetectionResult
	r, err = ld.client.Detect(ctx, req.Text)
	if err != nil {
		return
	}
	b, _ := json.Marshal(r)
	ld.logger.Debug(string(b))

	lang := ""
	confidence := 0.0
	for _, cv := range r {
		if !cv.Reliable {
			continue
		}

		l := strings.ToUpper(cv.Language)
		c := float64(cv.Confidence)
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
