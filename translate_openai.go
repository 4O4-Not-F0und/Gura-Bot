package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/sirupsen/logrus"
)

// OpenAITranslator implements the translation logic using the OpenAI style API.
// It embeds baseTranslator for common functionalities.
type OpenAITranslatorInstance struct {
	name         string
	aiClient     openai.Client
	systemPrompt string
	model        string
}

// newOpenAITranslator creates and initializes a new OpenAITranslator.
// It validates the provided TranslateConfig and configures the OpenAI client,
// language detector, rate limiter, and other parameters.
// Returns an error if any critical configuration is missing or invalid.
func newOpenAITranslator(conf TranslatorInstanceConfig) (c *OpenAITranslatorInstance, err error) {
	openaiOpts := []option.RequestOption{}

	if conf.Token == "" {
		logrus.Warn("no API token configured, using empty")
	} else {
		openaiOpts = append(openaiOpts, option.WithAPIKey(conf.Token))
	}
	if conf.Endpoint != "" {
		openaiOpts = append(openaiOpts, option.WithBaseURL(conf.Endpoint))
	}

	if conf.Model == "" {
		err = fmt.Errorf("no openai model configured")
		return
	}

	c = new(OpenAITranslatorInstance)
	c.aiClient = openai.NewClient(openaiOpts...)
	c.model = conf.Model

	// Already validated, just set it
	c.name = conf.Name
	c.systemPrompt = conf.SystemPrompt

	logrus.Debugf("initialized OpenAI translator with model: %s, api url: %s",
		c.model, conf.Endpoint)
	return
}

func (t *OpenAITranslatorInstance) Name() string {
	return t.name
}

// Translate sends the given text to the OpenAI API for translation.
// It respects the configured timeout and rate limiter.
// Returns the API's chat completion response or an error.
func (t *OpenAITranslatorInstance) Translate(ctx context.Context, text string) (resp *TranslateResponse, err error) {
	var chatCompletion *openai.ChatCompletion
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

	if err != nil {
		var apiErr = new(openai.Error)
		if errors.As(err, &apiErr) {
			// Mask sensitive data
			req := apiErr.Request.Clone(context.Background())
			req.Header = apiErr.Request.Header.Clone()
			req.Header.Set("Authorization", "********")
			err = fmt.Errorf("%w", &TranslateError{
				e:        err,
				Request:  req,
				Response: apiErr.Response,
			})
		}
		return
	}

	resp = new(TranslateResponse)
	if len(chatCompletion.Choices) > 0 {
		resp.Text = chatCompletion.Choices[0].Message.Content
		resp.TokenUsage.Completion = chatCompletion.Usage.CompletionTokens
		resp.TokenUsage.Prompt = chatCompletion.Usage.PromptTokens
		return
	}
	err = fmt.Errorf("no choice found in response")
	return
}
