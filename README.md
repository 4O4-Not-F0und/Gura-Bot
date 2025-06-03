[![Go Report Card](https://goreportcard.com/badge/github.com/4O4-Not-F0und/Gura-Bot)](https://goreportcard.com/report/github.com/4O4-Not-F0und/Gura-Bot)

# Telegram Translate Bot

Gura-Bot is a Go-based Telegram bot that automatically detects the language of incoming messages in specified chats and translates them into any configured languages using an OpenAI-compatible API.

## Features

* **Automatic Language Detection**: Identifies the language of incoming messages using [lingua-go](https://github.com/pemistahl/lingua-go).
* **AI Text Translation**: Translates detected text using any AI models via OpenAI-compatible endpoints.
* **Multiple Translator Support**: Can be configured with multiple translation service instances (e.g., different models or API providers).
* **Weighted Round-Robin & Failover**: Distributes translation load across configured translators using a smooth weighted round-robin algorithm and implements a failover mechanism with cooldown periods for temporarily or permanently disabling misbehaving instances.
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

* A Telegram Bot API token.
* Access to an OpenAI-compatible API endpoint and a corresponding API key (e.g., for models like GPT, Claude, Gemini if accessed via a compatible proxy or service).

### Running the Bot

#### Docker Compose

Refer to the `docker-compose.yml` file for an example setup. Ensure your `config.yml` is correctly volume-mounted into the container.

## Metrics

The bot exposes Prometheus metrics on the address specified in `metric.listen` (default path: `/metrics`).

Metrics include:

* `gura_bot_messages_total` (Gauge): Current number of messages being processed by the bot.
    * Labels: `state`, `chat_type`
    * States: `pending` (waiting for an available worker), `processing` (actively handled), `unauthorized` (disallowed source), `failed` (error during handling), `processed` (successfully handled).
* `gura_bot_translator_tasks_total` (Gauge): Total number of translation tasks, by state and translator.
    * Labels: `state`, `translator_name`
    * States: `pending` (waiting for rate limiter), `processing` (waiting for API response), `success` (translation successful), `failed` (any step in translation failed).
* `gura_bot_translator_tokens_used` (Counter): Used tokens for translation tasks, by token type and translator.
    * Labels: `token_type`, `translator_name`
    * Token Types: `completion` (output tokens), `prompt` (input tokens)
* `gura_bot_translator_up` (Gauge): Indicates if a translator is currently up and operational (1 for up, 0 for disabled due to failover).
    * Labels: `translator_name`
* `gura_bot_translator_selection_total` (Counter): Times a specific translator instance was chosen by the load balancing algorithm.
    * Labels: `translator_name`

## Contributing

Contributions, issues, and feature requests are welcome. Please open an issue to discuss your ideas before submitting a pull request.
