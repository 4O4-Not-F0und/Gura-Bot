package translate

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httputil"
	"slices"
	"time"

	"github.com/4O4-Not-F0und/Gura-Bot/metrics"
	"github.com/4O4-Not-F0und/Gura-Bot/selector"
	"github.com/pemistahl/lingua-go"
	"github.com/sirupsen/logrus"
)

const (
	translationStatePending    = "pending"
	translationStateProcessing = "processing"
	translationStateSuccess    = "success"
	translationStateFailed     = "failed"

	translationTokenUsedTypeCompletion = "completion"
	translationTokenUsedTypePrompt     = "prompt"

	translatorInstanceTypeOpenAI = "openai"
)

var (
	allTranslationTaskStates = []string{
		translationStatePending,
		translationStateProcessing,
		translationStateSuccess,
		translationStateFailed,
	}

	allTranslationTokenUsedTypes = []string{
		translationTokenUsedTypeCompletion,
		translationTokenUsedTypePrompt,
	}
)

type TranslateRequest struct {
	Text    string
	TraceId string
}

type TranslateResponse struct {
	Text           string
	TranslatorName string
	TokenUsage     struct {
		Completion int64
		Prompt     int64
	}
}

func (resp *TranslateResponse) CloneFrom(tr *TranslateResponse) {
	if tr == nil {
		return
	}

	resp.Text = tr.Text
	resp.TokenUsage = tr.TokenUsage
	resp.TranslatorName = tr.TranslatorName
}

type TranslateError struct {
	e        error
	Request  *http.Request
	Response *http.Response
}

func (r *TranslateError) DumpRequest(body bool) []byte {
	if r.Request.GetBody != nil {
		r.Request.Body, _ = r.Request.GetBody()
	}
	out, _ := httputil.DumpRequestOut(r.Request, body)
	return out
}

func (r *TranslateError) DumpResponse(body bool) []byte {
	out, _ := httputil.DumpResponse(r.Response, body)
	return out
}

func (r *TranslateError) Error() string {
	return r.e.Error()
}

type TranslatorInstance interface {
	Translate(context.Context, TranslateRequest) (*TranslateResponse, error)
	Name() string
}

// TranslateService provides common functionality for translators, primarily language detection.
type TranslateService struct {
	// set to negative or zero to disable retry
	maxmiumRetry            int
	retryCooldown           int
	sourceLangConf          SourceLanguageConfig
	detectorBuilder         lingua.LanguageDetectorBuilder
	defaultTranslatorConfig DefaultTranslatorConfig
	detector                lingua.LanguageDetector
	translatorSelector      selector.Selector[Translator]
}

func NewTranslateService(conf TranslateServiceConfig) (ts *TranslateService, err error) {
	ts = &TranslateService{
		maxmiumRetry: conf.MaxmiumRetry,
	}

	switch conf.TranslatorSelector {
	case selector.WRR:
		ts.translatorSelector = selector.NewWeightedRoundRobinSelector[Translator]()
	case selector.FALLBACK:
		ts.translatorSelector = selector.NewFallbackSelector[Translator]()
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

func (ts *TranslateService) initTranslatorEntries(translatorConfs []TranslatorConfig) (err error) {
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

		var instance TranslatorInstance
		switch tc.Type {
		case translatorInstanceTypeOpenAI:
			instance, err = newTranslatorInstanceOpenAI(tc)
		default:
			err = fmt.Errorf("unknown translator type: %s", tc.Type)
		}

		if err != nil {
			return
		}

		if slices.Contains(names, instance.Name()) {
			err = fmt.Errorf("duplicated translator name: %s", instance.Name())
			return
		}

		names = append(names, instance.Name())
		wtOpts := weightTranslatorOptions{
			TranslatorOptions: TranslatorOptions{
				Instance:         instance,
				Timeout:          tc.Timeout,
				UpMetric:         metrics.MetricTranslatorUp,
				SelectionMetric:  metrics.MetricTranslatorSelectionTotal,
				TasksMetric:      metrics.MetricTranslatorTasks,
				TokensUsedMetric: metrics.MetricTranslatorTokensUsed,
				FailoverConfig:   tc.Failover,
				RateLimitConfig:  tc.RateLimitConfig,
			},
			Weight: tc.Weight,
		}
		ts.translatorSelector.AddItem(newWeightedTranslator(wtOpts))
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

func (ts *TranslateService) Translate(req TranslateRequest) (resp *TranslateResponse, err error) {
	retry := 0
	logger := logrus.WithField("trace_id", req.TraceId)
	for {
		resp, err = ts.translate(req)
		if err == nil {
			return
		}

		if retry >= ts.maxmiumRetry {
			logger.Errorf("no more retries: maximum retries exceeded after %d attempts", retry)
			return
		}
		retry += 1
		logger.WithField("translator_name", resp.TranslatorName).
			Warnf("%v. Retry attempt %d/%d in %d seconds", err, retry, ts.maxmiumRetry, ts.retryCooldown)
		time.Sleep(time.Duration(ts.retryCooldown) * time.Second)
	}
}

func (ts *TranslateService) translate(req TranslateRequest) (resp *TranslateResponse, err error) {
	translator, err := ts.translatorSelector.Select()
	if err != nil {
		err = fmt.Errorf("error on select translator: %w", err)
		return
	}

	resp = &TranslateResponse{}
	var r *TranslateResponse
	r, err = translator.Translate(req)
	resp.CloneFrom(r)
	resp.TranslatorName = translator.GetName()
	if err != nil {
		return
	}
	return
}
