package translate

import (
	"fmt"
	"slices"
	"time"

	"github.com/4O4-Not-F0und/Gura-Bot/selector"
	"github.com/4O4-Not-F0und/Gura-Bot/translate/translator"
	"github.com/pemistahl/lingua-go"
	"github.com/sirupsen/logrus"
)

// TranslateService provides common functionality for translators, primarily language detection.
type TranslateService struct {
	// set to negative or zero to disable retry
	maxmiumRetry            int
	retryCooldown           int
	sourceLangConf          SourceLanguageConfig
	detectorBuilder         lingua.LanguageDetectorBuilder
	defaultTranslatorConfig translator.DefaultTranslatorConfig
	detector                lingua.LanguageDetector
	translatorSelector      selector.Selector[translator.Translator]
}

func NewTranslateService(conf TranslateServiceConfig) (ts *TranslateService, err error) {
	ts = &TranslateService{
		maxmiumRetry: conf.MaxmiumRetry,
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

	if conf.RetryCooldown <= 0 {
		err = fmt.Errorf("retry cooldown must be positive")
		return
	}
	ts.retryCooldown = conf.RetryCooldown

	if len(conf.DetectLangs) == 0 {
		err = fmt.Errorf("no detect languages configured")
		return
	}

	if len(conf.SourceLang.Langs) == 0 {
		err = fmt.Errorf("no source languages configured")
		return
	}

	if conf.SourceLang.ConfidenceThreshold <= 0 || conf.SourceLang.ConfidenceThreshold > 1 {
		err = fmt.Errorf("confidence threshold must in 0-1")
		return
	}
	ts.sourceLangConf = conf.SourceLang

	// No need to validate default config here
	ts.defaultTranslatorConfig = conf.DefaultTranslatorConfig

	allLanguages := map[string]lingua.Language{}
	availableLangs := []lingua.Language{}
	for _, l := range lingua.AllLanguages() {
		allLanguages[l.IsoCode639_1().String()] = l
	}

	for _, code := range conf.DetectLangs {
		if l, ok := allLanguages[code]; ok {
			logrus.Infof("found detect language: %s", code)
			availableLangs = append(availableLangs, l)
		} else {
			err = fmt.Errorf("unsupported language: %s", code)
			return
		}
	}

	ts.detectorBuilder = lingua.NewLanguageDetectorBuilder().
		FromLanguages(availableLangs...)
	ts.detector = ts.detectorBuilder.Build()

	// Initialize translators
	err = ts.initTranslatorEntries(conf.Translators)
	return
}

func (ts *TranslateService) initTranslatorEntries(translatorConfs []translator.TranslatorConfig) (err error) {
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
			err = fmt.Errorf("duplicated translator name: %s", t.GetName())
			return
		}

		names = append(names, t.GetName())
		ts.translatorSelector.AddItem(t)
	}
	logrus.Debugf("total weight of WRR entry: %d", ts.translatorSelector.TotalConfigWeight())
	return
}

// DetectLang attempts to detect the language of the given text.
// It returns the detected language (ISO 639-1 code), the confidence score,
// and an error if the detected language is not supported or confidence is too low.
func (ts *TranslateService) DetectLang(text string) (lang string, confidence float64, err error) {
	for _, cv := range ts.detector.ComputeLanguageConfidenceValues(text) {
		l := cv.Language().IsoCode639_1().String()
		c := cv.Value()
		if c > confidence {
			lang = l
			confidence = c
		}
	}

	if !slices.Contains(ts.sourceLangConf.Langs, lang) ||
		confidence < ts.sourceLangConf.ConfidenceThreshold {
		err = fmt.Errorf("supported language not detected")
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

		if retry >= ts.maxmiumRetry {
			logger.Errorf("no more retries: maximum retries exceeded after %d attempts", retry)
			return
		}
		retry += 1
		if name != "" {
			logger.WithField("translator_name", name).
				Warnf("%v. Retry attempt %d/%d in %d seconds", err, retry, ts.maxmiumRetry, ts.retryCooldown)
		} else {
			logger.Warnf("%v. Retry attempt %d/%d in %d seconds", err, retry, ts.maxmiumRetry, ts.retryCooldown)
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
