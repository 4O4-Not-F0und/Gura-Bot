package translate

import (
	"fmt"
	"slices"
	"time"

	"github.com/4O4-Not-F0und/Gura-Bot/selector"
	"github.com/4O4-Not-F0und/Gura-Bot/translate/detector"
	"github.com/4O4-Not-F0und/Gura-Bot/translate/translator"
	"github.com/sirupsen/logrus"
)

// TranslateService provides common functionality for translators, primarily language detection.
type TranslateService struct {
	// set to negative or zero to disable retry
	MaximumRetry             int
	retryCooldown            int
	defaultDetectorConfig    detector.DefaultDetectorConfig
	languageDetectorSelector selector.Selector[detector.LanguageDetector]
	defaultTranslatorConfig  translator.DefaultTranslatorConfig
	translatorSelector       selector.Selector[translator.Translator]
}

func NewTranslateService(conf TranslateServiceConfig) (ts *TranslateService, err error) {
	ts = &TranslateService{
		MaximumRetry: conf.MaximumRetry,
	}

	switch conf.TranslatorSelector {
	case selector.WRR:
		ts.translatorSelector = selector.NewWeightedRoundRobinSelector[translator.Translator]()
	case selector.FALLBACK:
		ts.translatorSelector = selector.NewFallbackSelector[translator.Translator]()
	default:
		err = fmt.Errorf("unrecognized translator selector: %s", conf.TranslatorSelector)
		return
	}

	switch conf.LanguageDetectorSelector {
	case selector.WRR:
		ts.languageDetectorSelector = selector.NewWeightedRoundRobinSelector[detector.LanguageDetector]()
	case selector.FALLBACK:
		ts.languageDetectorSelector = selector.NewFallbackSelector[detector.LanguageDetector]()
	default:
		err = fmt.Errorf("unrecognized language detector selector: %s", conf.LanguageDetectorSelector)
		return
	}

	if conf.RetryCooldown <= 0 {
		err = fmt.Errorf("retry cooldown must be positive")
		return
	}
	ts.retryCooldown = conf.RetryCooldown

	// No need to validate default config here
	ts.defaultTranslatorConfig = conf.DefaultTranslatorConfig
	ts.defaultDetectorConfig = conf.DefaultDetectorConfig

	// Initialize translators
	err = ts.initTranslators(conf.Translators)
	if err != nil {
		return
	}

	// Initialize language detectors
	err = ts.initDetectors(conf.LanguageDetectors)
	return
}

func (ts *TranslateService) initDetectors(detectorConfs []detector.DetectorConfig) (err error) {
	if len(detectorConfs) == 0 {
		err = fmt.Errorf("no detector configured")
		return
	}

	names := []string{}

	for _, dc := range detectorConfs {
		err = dc.CheckAndMergeDefaultConfig(ts.defaultDetectorConfig)
		if err != nil {
			return
		}

		var d detector.LanguageDetector
		d, err = detector.NewDetector(ts.languageDetectorSelector.GetType(), dc)
		if err != nil {
			return
		}

		if slices.Contains(names, d.GetName()) {
			err = fmt.Errorf("duplicated detector: %s", d.GetName())
			return
		}

		names = append(names, d.GetName())
		ts.languageDetectorSelector.AddItem(d)
	}
	logrus.Debugf("total weight of WRR entry: %d", ts.languageDetectorSelector.TotalConfigWeight())
	return
}

func (ts *TranslateService) initTranslators(translatorConfs []translator.TranslatorConfig) (err error) {
	if len(translatorConfs) == 0 {
		err = fmt.Errorf("no translator configured")
		return
	}

	names := []string{}

	for _, tc := range translatorConfs {
		err = tc.CheckAndMergeDefaultConfig(ts.defaultTranslatorConfig)
		if err != nil {
			return
		}

		var t translator.Translator
		t, err = translator.NewTranslator(ts.translatorSelector.GetType(), tc)
		if err != nil {
			return
		}

		if slices.Contains(names, t.GetName()) {
			err = fmt.Errorf("duplicated translator: %s", t.GetName())
			return
		}

		names = append(names, t.GetName())
		ts.translatorSelector.AddItem(t)
	}
	logrus.Debugf("total weight of WRR entry: %d", ts.translatorSelector.TotalConfigWeight())
	return
}

// DetectLang attempts to detect the language of the given text.
// It returns the detected language (ISO 639-1 code), the confidence score.
func (ts *TranslateService) DetectLang(req detector.DetectRequest) (resp *detector.DetectResponse, name string, err error) {
	retry := 0
	logger := logrus.WithField("trace_id", req.TraceId)
	for {
		resp, name, err = ts.detect(req)
		if err == nil {
			return
		}

		// WeakError shouldn't retry
		if detector.CheckWeakError(err) {
			return
		}

		if retry >= ts.MaximumRetry {
			logger.Errorf("no more retries: maximum retries exceeded after %d attempts", retry)
			return
		}
		retry += 1
		if name != "" {
			logger.WithField("detector_name", name).
				Warnf("%v. Retry attempt %d/%d in %d seconds", err, retry, ts.MaximumRetry, ts.retryCooldown)
		} else {
			logger.Warnf("%v. Retry attempt %d/%d in %d seconds", err, retry, ts.MaximumRetry, ts.retryCooldown)
		}
		time.Sleep(time.Duration(ts.retryCooldown) * time.Second)
	}
}

func (ts *TranslateService) detect(req detector.DetectRequest) (resp *detector.DetectResponse, name string, err error) {
	t, err := ts.languageDetectorSelector.Select()
	if err != nil {
		err = fmt.Errorf("error on select detector: %w", err)
		return
	}
	name = t.GetName()

	resp, err = t.Detect(req)
	if err != nil {
		return
	}
	return
}

func (ts *TranslateService) Translate(req translator.TranslateRequest) (resp *translator.TranslateResponse, name string, err error) {
	retry := 0
	logger := logrus.WithField("trace_id", req.TraceId)
	for {
		resp, name, err = ts.translate(req)
		if err == nil {
			return
		}

		if retry >= ts.MaximumRetry {
			logger.Errorf("no more retries: maximum retries exceeded after %d attempts", retry)
			return
		}
		retry += 1
		if name != "" {
			logger.WithField("translator_name", name).
				Warnf("%v. Retry attempt %d/%d in %d seconds", err, retry, ts.MaximumRetry, ts.retryCooldown)
		} else {
			logger.Warnf("%v. Retry attempt %d/%d in %d seconds", err, retry, ts.MaximumRetry, ts.retryCooldown)
		}
		time.Sleep(time.Duration(ts.retryCooldown) * time.Second)
	}
}

func (ts *TranslateService) translate(req translator.TranslateRequest) (resp *translator.TranslateResponse, name string, err error) {
	t, err := ts.translatorSelector.Select()
	if err != nil {
		err = fmt.Errorf("error on select translator: %w", err)
		return
	}
	name = t.GetName()

	resp, err = t.Translate(req)
	if err != nil {
		return
	}
	return
}
