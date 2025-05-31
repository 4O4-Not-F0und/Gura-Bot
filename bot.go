package main

import (
	"slices"
	"strconv"
	"sync"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/sirupsen/logrus"
)

const (
	messageHandleStatePending      = "pending"
	messageHandleStateUnauthorized = "unauthorized"
	messageHandleStateFailed       = "failed"
	messageHandleStateProcessed    = "processed"
	messageHandleStateProcessing   = "processing"
)

var (
	allMessageStates = []string{
		messageHandleStatePending,
		messageHandleStateUnauthorized,
		messageHandleStateProcessing,
		messageHandleStateProcessed,
		messageHandleStateFailed,
	}
)

type BotConfig struct {
	Token           string             `yaml:"token"`
	MessageSettings BotMessageSettings `yaml:"message_settings"`
	AllowedChats    []int64            `yaml:"allowed_chats"`
	WorkerPoolSize  int                `yaml:"worker_pool_size"`
}

type BotMessageSettings struct {
	DisableNotification bool `yaml:"disable_notification"`
	DisableLinkPreview  bool `yaml:"disable_link_preview"`
}

func newBotConfig() BotConfig {
	return BotConfig{
		MessageSettings: BotMessageSettings{},
		AllowedChats:    make([]int64, 0),
	}
}

type SafeSlice[T comparable] struct {
	*sync.RWMutex
	s []T
}

func newSafeSlice[T comparable](s []T) (ss *SafeSlice[T]) {
	ss = &SafeSlice[T]{
		RWMutex: new(sync.RWMutex),
	}
	ss.New(s)
	return
}

func (ss *SafeSlice[T]) Contains(elem T) bool {
	ss.RLock()
	defer ss.RUnlock()
	return slices.Contains(ss.s, elem)
}

func (ss *SafeSlice[T]) New(s []T) {
	ss.Lock()
	ss.s = slices.Clone(s)
	ss.Unlock()
}

type Bot struct {
	bot                           *tgbotapi.BotAPI
	translator                    *OpenAITranslator
	messageSettings               BotMessageSettings
	allowedChats                  *SafeSlice[int64]
	workerPoolSize                int
	reloadConfigMux               *sync.Mutex
	messageMetricsInitMux         *sync.Mutex
	messageMetricsInitialized     map[string]*sync.Once
	translationMetricsInitMux     *sync.Mutex
	translationMetricsInitialized map[string]*sync.Once
}

func newBot(config BotConfig, translator *OpenAITranslator) (bot *Bot, err error) {
	if config.Token == "" {
		logrus.Fatal("telegram bot token required")
	}

	if config.WorkerPoolSize <= 0 {
		logrus.Fatalf("invalid 'worker_pool_size': %d", config.WorkerPoolSize)
	}
	logrus.Info("authorizing telegram bot")

	var botApi *tgbotapi.BotAPI
	botApi, err = tgbotapi.NewBotAPI(config.Token)
	if err != nil {
		return
	}
	logrus.Infof("authorized on account: %s", botApi.Self.UserName)

	if logrus.StandardLogger().Level >= logrus.DebugLevel {
		botApi.Debug = true
	}

	bot = &Bot{
		bot:                           botApi,
		translator:                    translator,
		messageSettings:               config.MessageSettings,
		allowedChats:                  newSafeSlice(config.AllowedChats),
		workerPoolSize:                config.WorkerPoolSize,
		reloadConfigMux:               &sync.Mutex{},
		messageMetricsInitMux:         &sync.Mutex{},
		messageMetricsInitialized:     map[string]*sync.Once{},
		translationMetricsInitMux:     &sync.Mutex{},
		translationMetricsInitialized: map[string]*sync.Once{},
	}
	err = bot.ReloadConfig(config, translator)
	return
}

func (b *Bot) ReloadConfig(botConfig BotConfig, translator *OpenAITranslator) (err error) {
	logrus.Trace("acquiring bot.reloadConfigMux")
	b.reloadConfigMux.Lock()
	logrus.Trace("acquired bot.reloadConfigMux")

	b.allowedChats.New(botConfig.AllowedChats)
	b.messageSettings = botConfig.MessageSettings
	b.translator = translator
	if b.workerPoolSize != botConfig.WorkerPoolSize {
		logrus.Warn("worker pool size changed, please restart bot to apply")
	}

	b.reloadConfigMux.Unlock()
	logrus.Trace("released bot.reloadConfigMux")
	return
}

// ServeBot starts the bot's main loop for receiving and processing updates.
func (b *Bot) ServeBot() {
	q := make(chan int, b.workerPoolSize)
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := b.bot.GetUpdatesChan(u)

	logrus.Infof("begin update loop, timeout: %ds, queue size: %d", u.Timeout, b.workerPoolSize)
	for update := range updates {
		var msg *tgbotapi.Message
		if update.Message != nil {
			msg = update.Message
		} else if update.ChannelPost != nil {
			msg = update.ChannelPost
		} else {
			continue
		}

		chatIdStr := "unknown"
		if msg.Chat != nil {
			chatIdStr = strconv.FormatInt(msg.Chat.ID, 10)
		}
		b.checkOrInitMessageMetricsForChat(chatIdStr)

		var text string
		if len(msg.Text) > 0 {
			text = msg.Text
		} else if len(msg.Caption) > 0 {
			text = msg.Caption
		} else {
			logrus.WithField("chat_id", chatIdStr).Debug("message text undetected")
			continue
		}

		metricMessages.WithLabelValues(messageHandleStatePending, chatIdStr).Inc()
		logrus.Trace("acquiring queue")
		q <- 1
		metricMessages.WithLabelValues(messageHandleStatePending, chatIdStr).Dec()
		logrus.Trace("acquired queue")
		go func(m *tgbotapi.Message, t, c string) {
			metricMessages.WithLabelValues(messageHandleStateProcessing, chatIdStr).Inc()
			b.handleMessage(m, t, c)
			<-q
			logrus.Trace("released queue")
			metricMessages.WithLabelValues(messageHandleStateProcessing, chatIdStr).Dec()
		}(msg, text, chatIdStr)
	}
}

// handleMessage processes a single incoming Telegram message.
// It checks for authorization, extracts text, detects language,
// translates, and sends a reply.
func (b *Bot) handleMessage(message *tgbotapi.Message, text, chatIdStr string) {
	logger := logrus.WithField("chat_id", chatIdStr)
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("panic recovered in handleMessage: %v", r)
			b.onMessageHandleFailed(chatIdStr)
		}
	}()

	if message.From != nil {
		logger = logger.WithField("user_id", message.From.ID)
	}

	if !b.isAllowed(message) {
		metricMessages.WithLabelValues(messageHandleStateUnauthorized, chatIdStr).Inc()
		logger.Infoln("disallowed message source")
		return
	}

	lang, confidence, err := b.translator.DetectLang(text)
	logger = logger.WithFields(logrus.Fields{
		"lang":            lang,
		"lang_confidence": confidence,
	})
	if err != nil {
		logger.Warn(err)
		b.onMessageHandleFailed(chatIdStr)
		return
	}

	b.checkOrInitTranslationMetricsForChat(chatIdStr)
	resp, err := b.translator.Translate(text, chatIdStr)
	if err != nil {
		b.onTranslationFailed(chatIdStr)
		logger.Errorf("an error occured while translating: %v", err)
		return
	}
	logger = logger.WithFields(logrus.Fields{
		"usage_completion_tokens": resp.Usage.CompletionTokens,
		"usage_prompt_tokens":     resp.Usage.PromptTokens,
	})
	metricTranslationTokensUsed.WithLabelValues(
		translationTokenUsedTypeCompletion,
		chatIdStr,
	).Add(float64(resp.Usage.CompletionTokens))
	metricTranslationTokensUsed.WithLabelValues(
		translationTokenUsedTypePrompt,
		chatIdStr,
	).Add(float64(resp.Usage.PromptTokens))

	translatedText, err := b.translator.ParseChatResponse(resp)
	if err != nil {
		b.onTranslationFailed(chatIdStr)
		logger.Errorf("an error occured while parsing chat response: %v", err)
		return
	}
	metricTranslationTasks.WithLabelValues(translationStateSuccess, chatIdStr).Inc()

	msg := tgbotapi.NewMessage(message.Chat.ID, translatedText)
	msg.DisableNotification = b.messageSettings.DisableNotification
	msg.DisableWebPagePreview = b.messageSettings.DisableLinkPreview
	msg.ReplyToMessageID = message.MessageID

	_, err = b.bot.Send(msg)
	if err != nil {
		b.onMessageHandleFailed(chatIdStr)
		logger.Errorf("an error occured while sending message: %v", err)
	}
	logger.Info("completed")
	metricMessages.WithLabelValues(messageHandleStateProcessed, chatIdStr).Inc()
}

func (b *Bot) checkOrInitMessageMetricsForChat(chatIdStr string) {
	logger := logrus.WithField("chat_id", chatIdStr)

	logger.Trace("acquiring bot.messageMetricsInitMux")
	b.messageMetricsInitMux.Lock()
	logger.Trace("acquired bot.messageMetricsInitMux")
	once, ok := b.messageMetricsInitialized[chatIdStr]
	if !ok {
		once = &sync.Once{}
		b.messageMetricsInitialized[chatIdStr] = once
	}
	b.messageMetricsInitMux.Unlock()
	logger.Trace("released bot.messageMetricsInitMux")

	once.Do(func() {
		for _, state := range allMessageStates {
			metricMessages.WithLabelValues(state, chatIdStr).Set(0)
		}
		logger.Debugf("message metrics initialized")
	})
}

func (b *Bot) checkOrInitTranslationMetricsForChat(chatIdStr string) {
	logger := logrus.WithField("chat_id", chatIdStr)

	logger.Trace("acquiring  bot.translationMetricsInitMux")
	b.translationMetricsInitMux.Lock()
	logger.Trace("acquired bot.translationMetricsInitMux")

	once, ok := b.translationMetricsInitialized[chatIdStr]
	if !ok {
		once = &sync.Once{}
		b.translationMetricsInitialized[chatIdStr] = once
	}
	b.translationMetricsInitMux.Unlock()
	logger.Trace("released bot.ranslationMetricsInitMux")

	once.Do(func() {
		for _, state := range allTranslationTaskStates {
			metricTranslationTasks.WithLabelValues(state, chatIdStr).Set(0)
		}
		for _, t := range allTranslationTokenUsedTypes {
			metricTranslationTokensUsed.WithLabelValues(t, chatIdStr).Add(0.0)
		}
		logger.Debugf("translation tasks metrics initialized")
	})
}

func (b *Bot) onTranslationFailed(chatIdStr string) {
	b.onMessageHandleFailed(chatIdStr)
	metricTranslationTasks.WithLabelValues(translationStateFailed, chatIdStr).Inc()
}

func (b *Bot) onMessageHandleFailed(chatIdStr string) {
	metricMessages.WithLabelValues(messageHandleStateFailed, chatIdStr).Inc()
}

func (b *Bot) isAllowed(message *tgbotapi.Message) bool {
	if message.Chat.Type == "private" {
		return b.allowedChats.Contains(message.From.ID)
	}
	return b.allowedChats.Contains(message.Chat.ID)
}
