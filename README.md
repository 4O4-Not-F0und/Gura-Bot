[![Go Report Card](https://goreportcard.com/badge/github.com/4O4-Not-F0und/Gura-Bot)](https://goreportcard.com/report/github.com/4O4-Not-F0und/Gura-Bot)

# Gura Bot

Gura-Bot is a Go-based Telegram bot that automatically detects the language of incoming messages in specified chats and translates them into any configured languages using an OpenAI-compatible API.

## Features

* **Automatic Language Detection**: Identifies the language of incoming messages.
* **AI Text Translation**: Translates detected text using any AI models via OpenAI-compatible APIs.
* **Multiple Provider Support**:
    * Language Detectors: `Lingua` (local), `detectlanguage.com` API.
    * Translators: OpenAI-compatible APIs.
* **Flexible Service Selection**:
    * `fallback`: Tries services in a predefined order.
    * `wrr` (Weighted Round Robin): Distributes load based on configured weights.
* **Failover**: Distributes work load and implements a failover mechanism with cooldown periods for temporarily or permanently disabling misbehaving instances.
* **Authorization**: Restricts bot usage to pre-approved Telegram chat IDs or user IDs.
* **Rate Limiting**: Manages API request rates per translator instance to stay within provider limits.
* **Concurrent Processing**: Handles multiple translation requests simultaneously using a configurable worker pool.
* **Prometheus Metrics**: Exposes key operational metrics for monitoring.
* **Customizable Translation Prompt**: Allows fine-tuning of translation behavior via a detailed system prompt, configurable globally or per translator instance.
* **Configuration Reloading**: Supports hot reloading of most configuration settings via `SIGHUP` signal.

## Configuration

The bot is configured using a `config.yml` file. An example configuration is provided in `config.example.yml`.

### Command-line Flags

* `-config <path>`: Path to the configuration file. Default: `config.yml`.

### Configuration Reloading

This application supports dynamic configuration reloading, allowing updates to most settings without a restart.

To reload the configuration, send a `SIGHUP` signal to the running bot process:

```bash
killall -s HUP gura_bot
```
Or, if using the provided Docker image:
```bash
docker exec -it <CONTAINER_NAME_OR_ID> reload.sh
```

Upon receiving the `SIGHUP` signal, the bot will attempt to reload its configuration from the `config.yml` file.

#### What Cannot Be Reloaded (Requires a Restart)

The following settings require a full application restart to take effect:

* **Bot API Token**:
    * `bot.token`: The Telegram Bot API token is initialized at startup.
* **Metric Server Listen Address**:
    * `metric.listen`: The address and port for the Prometheus metrics server.

## Usage

### Prerequisites

* **Telegram Bot API token**: Obtain this from BotFather on Telegram.
* **External Language Detection Service (Optional)**: If using external services for detection (e.g., `detectlanguage.com` API), you'll need their respective API keys/tokens.
* Access to an OpenAI-compatible API endpoint and a corresponding API key (e.g., for models like GPT, Claude, Gemini if accessed via a compatible proxy or service).

### Running the Bot

#### Docker Compose

Refer to the `docker-compose.yml` file for an example setup. Ensure your `config.yml` is correctly volume-mounted into the container.

## Metrics

The bot exposes Prometheus metrics on the address specified in `metric.listen` (default path: `/metrics`).

Metrics include:

* `gura_bot_messages_total{state, chat_type}` (Gauge): Current number of messages being processed by the bot.
    * States:
        * `pending`: waiting for an available worker.
        * `processing`: actively handled.
        * `unauthorized`: disallowed source.
        * `failed`: error during handling.
        * `processed`: successfully handled.
* `gura_bot_translator_tasks_total{state, translator_name}` (Gauge): Total number of translation tasks, by state and translator.
    * States:
        * `pending`: waiting for rate limiter.
        * `processing`: waiting for response.
        * `success`: translation successful.
        * `failed`: any step in translation failed.
* `gura_bot_translator_tokens_used{token_type, translator_name}` (Counter): Used tokens for translation tasks, by token type and translator.
    * Token Types:
        * `completion`: output tokens.
        * `prompt`: input tokens.
* `gura_bot_translator_up{translator_name}` (Gauge): Indicates if a translator is currently up and operational (1 for up, 0 for disabled due to failover).
* `gura_bot_translator_selection_total{translator_name}` (Counter): Times a specific translator instance was selected.
* `gura_bot_detector_tasks_total{state, detector_name}` (Gauge): Total number of language detection tasks by state and detector instance name.
    * States: Refer to `gura_bot_translator_tasks_total`
* `gura_bot_detector_up{detector_name}` (Gauge): Indicates if a detector is operational.
* `gura_bot_detector_selection_total{detector_name}` (Counter): Times each detector instance was selected.

## Contributing

Contributions, issues, and feature requests are welcome. Please open an issue to discuss your ideas before submitting a pull request.
