package main

import (
	"strconv"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/sirupsen/logrus"
)

type Message struct {
	*tgbotapi.Message
	logger   *logrus.Entry
	Content  string
	ChatId   string
	ChatType string
}

func newMessage(message *tgbotapi.Message) *Message {
	logger := logrus.WithFields(logrus.Fields{
		"chat_type": message.Chat.Type,
		"chat_id":   message.Chat.ID,
	})

	if message.From != nil {
		logger = logger.WithField("user_id", message.From.ID)
	}

	var text string
	if len(message.Text) > 0 {
		text = message.Text
	} else if len(message.Caption) > 0 {
		text = message.Caption
	}

	m := &Message{
		Message:  message,
		logger:   logger,
		Content:  text,
		ChatType: message.Chat.Type,
		ChatId:   strconv.FormatInt(message.Chat.ID, 10),
	}

	return m
}

func (m *Message) onMessageHandleFailed() {
	metricMessages.WithLabelValues(messageHandleStateFailed, m.ChatType).Inc()
	m.onProcessed()
}

func (m *Message) onUnauthorized() {
	metricMessages.WithLabelValues(messageHandleStateUnauthorized, m.ChatType).Inc()
	m.onProcessed()
	m.logger.Infoln("disallowed message source")
}

func (m *Message) onPending() {
	metricMessages.WithLabelValues(messageHandleStatePending, m.ChatType).Inc()
}

func (m *Message) onProcessing() {
	metricMessages.WithLabelValues(messageHandleStatePending, m.ChatType).Dec()
	metricMessages.WithLabelValues(messageHandleStateProcessing, m.ChatType).Inc()
}

func (m *Message) onSuccess() {
	metricMessages.WithLabelValues(messageHandleStateProcessed, m.ChatType).Inc()
	m.onProcessed()
}

func (m *Message) onProcessed() {
	metricMessages.WithLabelValues(messageHandleStateProcessing, m.ChatType).Dec()
}
