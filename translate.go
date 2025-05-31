package main

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
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

// TranslateConfig holds all configuration related to translation services.
type TranslateConfig struct {
	DetectLangs []string             `yaml:"detect_langs"`
	SourceLang  SourceLanguageConfig `yaml:"source_lang"`
	Translator  TranslatorConfig     `yaml:"translator"`
}

// SourceLanguageConfig defines parameters for validating detected source languages.
type SourceLanguageConfig struct {
	ConfidenceThreshold float64  `yaml:"confidence_threshold"`
	Langs               []string `yaml:"langs"`
}

// TranslatorConfig holds settings for the translation provider (e.g., OpenAI).
type TranslatorConfig struct {
	Endpoint  string                    `yaml:"endpoint"`
	Model     string                    `yaml:"model"`
	Token     string                    `yaml:"token"`
	Prompt    string                    `yaml:"prompt"`
	Timeout   int64                     `yaml:"timeout"`
	RateLimit TranslatorRateLimitConfig `yaml:"rate_limit"`
}

// TranslatorRateLimitConfig defines the parameters for the rate limiter.
type TranslatorRateLimitConfig struct {
	BucketSize int     `yaml:"bucket_size"`
	RefillTPS  float64 `yaml:"refill_token_per_sec"`
}

// newTranslateConfig creates a new TranslateConfig with default empty slices and zero values.
func newTranslateConfig() (c TranslateConfig) {
	return TranslateConfig{
		DetectLangs: make([]string, 0),
		SourceLang: SourceLanguageConfig{
			ConfidenceThreshold: 0,
			Langs:               make([]string, 0),
		},
		Translator: TranslatorConfig{},
	}
}

// baseTranslator provides common functionality for translators, primarily language detection.
type baseTranslator struct {
	sourceLangConf  SourceLanguageConfig
	detectorBuilder lingua.LanguageDetectorBuilder
	timeout         time.Duration
	limiter         *rate.Limiter
}

// DetectLang attempts to detect the language of the given text.
// It returns the detected language (ISO 639-1 code), the confidence score,
// and an error if the detected language is not supported or confidence is too low.
func (t *baseTranslator) DetectLang(text string) (lang string, confidence float64, err error) {
	detector := t.detectorBuilder.Build()
	for _, cv := range detector.ComputeLanguageConfidenceValues(text) {
		l := cv.Language().IsoCode639_1().String()
		c := cv.Value()
		if c > confidence {
			lang = l
			confidence = c
		}
	}

	if !slices.Contains(t.sourceLangConf.Langs, lang) ||
		confidence < t.sourceLangConf.ConfidenceThreshold {
		err = fmt.Errorf("supported language not detected")
	}

	return
}

// OpenAITranslator implements the translation logic using the OpenAI style API.
// It embeds baseTranslator for common functionalities.
type OpenAITranslator struct {
	baseTranslator
	aiClient     openai.Client
	systemPrompt string
	model        string
}

// newOpenAITranslator creates and initializes a new OpenAITranslator.
// It validates the provided TranslateConfig and configures the OpenAI client,
// language detector, rate limiter, and other parameters.
// Returns an error if any critical configuration is missing or invalid.
func newOpenAITranslator(translateConf TranslateConfig) (c *OpenAITranslator, err error) {
	c = new(OpenAITranslator)
	openaiOpts := []option.RequestOption{}

	if translateConf.Translator.Endpoint == "" {
		logrus.Info("no OpenAI endpoint configured, using default endpoint")
	} else {
		openaiOpts = append(openaiOpts, option.WithBaseURL(translateConf.Translator.Endpoint))
	}

	if translateConf.Translator.Token == "" {
		logrus.Warn("no API token configured, using empty")
	} else {
		openaiOpts = append(openaiOpts, option.WithAPIKey(translateConf.Translator.Token))
	}
	c.aiClient = openai.NewClient(openaiOpts...)

	if translateConf.Translator.Model == "" {
		err = fmt.Errorf("no openai model configured")
		return
	}
	c.model = translateConf.Translator.Model

	if translateConf.Translator.Prompt == "" {
		err = fmt.Errorf("no system prompt configured")
		return
	}
	c.systemPrompt = translateConf.Translator.Prompt

	if len(translateConf.DetectLangs) == 0 {
		err = fmt.Errorf("no detect languages configured")
		return
	}

	if len(translateConf.SourceLang.Langs) == 0 {
		err = fmt.Errorf("no source languages configured")
		return
	}

	if translateConf.SourceLang.ConfidenceThreshold == 0 {
		err = fmt.Errorf("confidence threshold is zero")
		return
	}
	c.sourceLangConf = translateConf.SourceLang

	if translateConf.Translator.Timeout == 0 {
		err = fmt.Errorf("translator timeout is zero")
		return
	}
	c.timeout = time.Duration(translateConf.Translator.Timeout) * time.Second

	if translateConf.Translator.RateLimit.RefillTPS == 0.0 {
		err = fmt.Errorf("translator limiter refill rate is zero")
		return
	}

	if translateConf.Translator.RateLimit.BucketSize == 0 {
		err = fmt.Errorf("translator limiter bucket size is zero")
		return
	}
	c.limiter = rate.NewLimiter(
		rate.Limit(translateConf.Translator.RateLimit.RefillTPS),
		translateConf.Translator.RateLimit.BucketSize,
	)

	logrus.Infof("using model: %s, api url: %s", translateConf.Translator.Model, translateConf.Translator.Endpoint)
	logrus.Infof(
		"rate limiter refill: %.2f tokens/s, bucket size: %d",
		translateConf.Translator.RateLimit.RefillTPS,
		translateConf.Translator.RateLimit.BucketSize,
	)

	allLanguages := map[string]lingua.Language{}
	availableLangs := []lingua.Language{}
	for _, l := range lingua.AllLanguages() {
		allLanguages[l.IsoCode639_1().String()] = l
	}

	for _, code := range translateConf.DetectLangs {
		if l, ok := allLanguages[code]; ok {
			logrus.Infof("found detect language: %s", code)
			availableLangs = append(availableLangs, l)
		} else {
			err = fmt.Errorf("unsupported language: %s", code)
			return
		}
	}

	c.detectorBuilder = lingua.NewLanguageDetectorBuilder().
		FromLanguages(availableLangs...)
	return
}

// Translate sends the given text to the OpenAI API for translation.
// It respects the configured timeout and rate limiter.
// Returns the API's chat completion response or an error.
func (t *OpenAITranslator) Translate(text, chatIdStr string) (chatCompletion *openai.ChatCompletion, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), t.timeout)
	defer cancel()

	logrus.Trace("wating for limiter")
	metricTranslationTasks.WithLabelValues(translationStatePending, chatIdStr).Inc()
	err = t.limiter.Wait(ctx)
	metricTranslationTasks.WithLabelValues(translationStatePending, chatIdStr).Dec()
	if err != nil {
		return nil, fmt.Errorf("rate limiter wait failed: %w", err)
	}
	metricTranslationTasks.WithLabelValues(translationStateProcessing, chatIdStr).Inc()
	defer metricTranslationTasks.WithLabelValues(translationStateProcessing, chatIdStr).Dec()

	logrus.Trace("wating for translate response")
	chatCompletion, err = t.aiClient.Chat.Completions.New(
		ctx,
		openai.ChatCompletionNewParams{
			Model: t.model,
			Messages: []openai.ChatCompletionMessageParamUnion{
				openai.SystemMessage(t.systemPrompt),
				openai.UserMessage(text),
			},
		},
	)
	return
}

// ParseChatResponse extracts the translated text content from an OpenAI ChatCompletion response.
// Returns the translated text or an error if no suitable choice is found in the response.
func (t *OpenAITranslator) ParseChatResponse(chatCompletion *openai.ChatCompletion) (ret string, err error) {
	if len(chatCompletion.Choices) > 0 {
		ret = chatCompletion.Choices[0].Message.Content
		return
	}
	err = fmt.Errorf("no choice found in response")
	return
}
