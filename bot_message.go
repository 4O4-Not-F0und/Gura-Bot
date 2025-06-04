package main

import (
	"crypto/md5"
	"fmt"
	"strconv"

	"github.com/4O4-Not-F0und/Gura-Bot/metrics"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/sirupsen/logrus"
)

type Message struct {
	*tgbotapi.Message
	logger   *logrus.Entry
	Content  string
	ChatId   string
	ChatType string
	TraceId  string
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
	m.TraceId = m.traceId()
	m.logger = m.logger.WithField("trace_id", m.TraceId)
	return m
}

func (m *Message) traceId() string {
	h := md5.New()
	var b []byte
	h.Write(fmt.Appendf(b, "%s%d", m.ChatId, m.MessageID))
	return fmt.Sprintf("%x", h.Sum(nil))
}

func (m *Message) onMessageHandleFailed() {
	metrics.MetricMessages.WithLabelValues(messageHandleStateFailed, m.ChatType).Inc()
	m.onProcessed()
}

func (m *Message) onUnauthorized() {
	metrics.MetricMessages.WithLabelValues(messageHandleStateUnauthorized, m.ChatType).Inc()
	m.onProcessed()
	m.logger.Infoln("disallowed message source")
}

func (m *Message) onPending() {
	metrics.MetricMessages.WithLabelValues(messageHandleStatePending, m.ChatType).Inc()
}

func (m *Message) onProcessing() {
	metrics.MetricMessages.WithLabelValues(messageHandleStatePending, m.ChatType).Dec()
	metrics.MetricMessages.WithLabelValues(messageHandleStateProcessing, m.ChatType).Inc()
}

func (m *Message) onSuccess() {
	metrics.MetricMessages.WithLabelValues(messageHandleStateProcessed, m.ChatType).Inc()
	m.onProcessed()
}

func (m *Message) onProcessed() {
	metrics.MetricMessages.WithLabelValues(messageHandleStateProcessing, m.ChatType).Dec()
}
