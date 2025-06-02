package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httputil"
	"slices"
	"time"

	"github.com/pemistahl/lingua-go"
	"github.com/sirupsen/logrus"
	"golang.org/x/time/rate"
)

const (
	translationStatePending    = "pending"
	translationStateProcessing = "processing"
	translationStateSuccess    = "success"
	translationStateFailed     = "failed"

	translationTokenUsedTypeCompletion = "completion"
	translationTokenUsedTypePrompt     = "prompt"

	translationLimiterWaitSeconds = 30
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
	Text     string
	ChatType string
	ChatId   string
}

type TranslateResponse struct {
	Text       string
	TokenUsage struct {
		Completion int64
		Prompt     int64
	}
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
	Translate(string) (*TranslateResponse, error)
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
	translatorEntry         TranslatorEntry
	limiter                 *rate.Limiter
}

func newTranslateService(conf TranslateServiceConfig) (ts *TranslateService, err error) {
	ts = &TranslateService{
		maxmiumRetry:    conf.MaxmiumRetry,
		translatorEntry: newWeightedTranslatorEntry(),
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

	if conf.GlobalRateLimit.RefillTPS <= 0.0 {
		err = fmt.Errorf("translator limiter refill rate must be positive")
		return
	}

	if conf.GlobalRateLimit.BucketSize <= 0 {
		err = fmt.Errorf("translator limiter bucket size must be positive")
		return
	}

	ts.limiter = rate.NewLimiter(
		rate.Limit(conf.GlobalRateLimit.RefillTPS),
		conf.GlobalRateLimit.BucketSize,
	)
	logrus.Infof(
		"global rate limiter refill: %.2f tokens/s, bucket size: %d",
		conf.GlobalRateLimit.RefillTPS,
		conf.GlobalRateLimit.BucketSize,
	)

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

func (ts *TranslateService) initTranslatorEntries(translatorConfs []TranslatorInstanceConfig) (err error) {
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
			instance, err = newOpenAITranslator(tc)
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
		ts.translatorEntry.AddInstance(TranslatorEntryInstanceOptions{
			Instance:        instance,
			Weight:          tc.Weight,
			UpMetric:        metricTranslatorUp,
			SelectionMetric: metricTranslatorSelectionTotal,
			FailoverConfig:  tc.Failover,
		})

		for _, state := range allTranslationTaskStates {
			metricTranslatorTasks.WithLabelValues(state, instance.Name()).Add(0.0)
		}

		for _, t := range allTranslationTokenUsedTypes {
			metricTranslatorTokensUsed.WithLabelValues(t, instance.Name()).Add(0.0)
		}
	}
	logrus.Debugf("total weight of WRR entry: %d", ts.translatorEntry.TotalConfigWeight())
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
	logger := logrus.WithField("chat_id", req.ChatId)
	for {
		resp, err = ts.translate(req, logger)
		if err == nil {
			return
		}

		if retry >= ts.maxmiumRetry {
			logger.Errorf("no more retries: maximum retries exceeded after %d attempts", retry)
			return
		}
		retry += 1
		logger.Warnf("%v. Retry attempt %d/%d in %d seconds", err, retry, ts.maxmiumRetry, ts.retryCooldown)
		time.Sleep(time.Duration(ts.retryCooldown) * time.Second)
	}
}

func (ts *TranslateService) translate(req TranslateRequest, logger *logrus.Entry) (resp *TranslateResponse, err error) {
	translator, err := ts.translatorEntry.Translator()
	if err != nil {
		err = fmt.Errorf("error on select translator: %w", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), translationLimiterWaitSeconds*time.Second)
	defer cancel()

	tName := translator.InstanceName()
	logger = logger.WithField("translator_name", tName)

	logger.Trace("wating for global limiter")
	metricTranslatorTasks.WithLabelValues(translationStatePending, tName).Inc()
	err = ts.limiter.Wait(ctx)
	metricTranslatorTasks.WithLabelValues(translationStatePending, tName).Dec()
	if err != nil {
		return nil, fmt.Errorf("rate limiter wait failed: %w", err)
	}
	metricTranslatorTasks.WithLabelValues(translationStateProcessing, tName).Inc()
	defer metricTranslatorTasks.WithLabelValues(translationStateProcessing, tName).Dec()

	logger.Trace("wating for translate response")
	resp, err = translator.Translate(req.Text)
	if resp != nil {
		metricTranslatorTokensUsed.WithLabelValues(
			translationTokenUsedTypeCompletion, tName,
		).Add(float64(resp.TokenUsage.Completion))
		metricTranslatorTokensUsed.WithLabelValues(
			translationTokenUsedTypePrompt, tName,
		).Add(float64(resp.TokenUsage.Prompt))
	}

	if err != nil {
		metricTranslatorTasks.WithLabelValues(translationStateFailed, tName).Inc()
		return
	}
	metricTranslatorTasks.WithLabelValues(translationStateSuccess, tName).Inc()
	return
}
