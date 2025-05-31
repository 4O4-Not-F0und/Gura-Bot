# Telegram Translate Bot

Telegram-Translate-Bot is a Go-based Telegram bot that automatically detects the language of incoming messages in specified chats and translates them into English using an OpenAI-compatible API.

## Features

* **Automatic Language Detection**: Identifies the language of incoming messages.
* **AI Text Translation**: Translates detected text using any AI models via OpenAI-compatible endpoint.
* **Authorization**: Restricts bot usage to pre-approved Telegram chats or user IDs.
* **Rate Limiting**: Manages API request rates to stay within provider limits.
* **Concurrent Processing**: Handles multiple translation requests simultaneously using a worker pool.
* **Prometheus Metrics**: Exposes key operational metrics for monitoring.
* **Customizable Translation Prompt**: Allows fine-tuning of translation behavior via a detailed system prompt.

## Configuration

The bot is configured using a `config.yml` file. An example configuration is provided in `config.example.yml`.

### Command-line Flags

  * `-config <path>`: Path to the configuration file. Default: `config.yml`.

### Configuration Reloading

This application supports dynamic configuration reloading, allowing to update certain settings without a restart.

#### How to Trigger a Reload

To reload the configuration, send a `SIGHUP` signal to the running bot process:

```bash
killall -s HUP telegram_translate_bot
```

Upon receiving the `SIGHUP` signal, the bot will attempt to reload its configuration from the `config.yml` file.

#### What Cannot Be Reloaded (Requires a Restart)

The following settings require a full application restart to take effect:

* **Bot Worker Pool Size**:

    * `bot.worker_pool_size`: Changes to the number of concurrent message processing workers require a restart.

* **Metric Server Listen Address**:

    * `metric.listen`: The address and port for the Prometheus metrics server is configured at startup and cannot be changed via hot reload.

## Usage

### Prerequisites

  * Access to a Telegram Bot API token
  * Access to an OpenAI-compatible API (e.g., Google AI Studio for Gemini API key)

### Running the Bot

### Docker Compose

Refer to `docker-compose.yml`.

## Metrics

The bot exposes Prometheus metrics on the address specified in `metric.listen` with default `/metrics` path.

Metrics include:

  * `telegram_translate_bot_messages{state=<state>, chat_id=<chat_id>}` (Gauge):

     Current number of messages being processed by the bot. The `state` label can be one of the following:

     * `pending`: Waiting for available worker.
     * `processing`: Messages in processing.
     * `unauthorized`: Messages from unauthorized chats.
     * `failed`: Messages where an error occurred while processing.
     * `processed`: Messages processed successfully.

  * `telegram_translate_bot_translation_tasks_total{state=<state>, chat_id=<chat_id>}` (Gauge):

     Total number of translation tasks. The `state` label can be one of the following:

     * `pending`: Waiting for rate limiter.
     * `processing`: Tasks in processing.
     * `failed`: Tasks where an error occurred while processing.
     * `success`: Tasks processed successfully.

  * `telegram_translate_bot_translation_tokens_used{type=<type>, chat_id=<chat_id>}` (Counter):

     Used tokens of translation tasks. The `type` label can be one of the following:

     * `completion`: Tokens used in completion.
     * `prompt`: Tokens used in prompt.

## Contributing

Contributions, issues, and feature requests are welcome. Please open an issue to discuss your ideas before submitting a pull request.
