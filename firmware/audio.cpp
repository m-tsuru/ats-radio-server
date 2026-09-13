#include "audio.h"

#include <Arduino.h>
#include <WiFi.h>

#if __has_include("config.h")
#include "config.h"
#else
#include "config.example.h"
#endif

#if AUDIO_CAPTURE_BACKEND == AUDIO_CAPTURE_I2S
#include <driver/i2s_std.h>
static_assert(AUDIO_BITS == 16, "the initial I2S backend supports 16-bit PCM only");
static_assert(AUDIO_CHANNELS == 1, "the initial I2S backend supports mono PCM only");
#endif

namespace {
bool stream_requested = false;
WiFiServer audio_server(AUDIO_PORT, 1);
WiFiClient audio_client;

#if AUDIO_CAPTURE_BACKEND == AUDIO_CAPTURE_I2S
constexpr size_t RING_SIZE = 8192;
i2s_chan_handle_t rx_channel = nullptr;
uint8_t ring_buffer[RING_SIZE];
size_t ring_head = 0;
size_t ring_tail = 0;
size_t ring_used = 0;

void ringPush(const uint8_t* data, size_t size) {
  for (size_t i = 0; i < size; ++i) {
    ring_buffer[ring_head] = data[i];
    ring_head = (ring_head + 1) % RING_SIZE;
  }
  ring_used += size;
}

void captureAudio() {
  if (!rx_channel || ring_used >= RING_SIZE) return;
  uint8_t input[512];
  size_t bytes_read = 0;
  const size_t capacity = min(sizeof(input), RING_SIZE - ring_used);
  if (i2s_channel_read(rx_channel, input, capacity, &bytes_read, 0) == ESP_OK && bytes_read) {
    ringPush(input, bytes_read);
  }
}

void sendAudio() {
  if (!audio_client.connected() || !ring_used) return;
  const int writable = audio_client.availableForWrite();
  if (writable <= 0) return;
  size_t contiguous = min(ring_used, RING_SIZE - ring_tail);
  contiguous = min(contiguous, static_cast<size_t>(writable));
  const size_t written = audio_client.write(ring_buffer + ring_tail, contiguous);
  ring_tail = (ring_tail + written) % RING_SIZE;
  ring_used -= written;
}
#endif
}  // namespace

void audioBegin() {
#if AUDIO_CAPTURE_BACKEND == AUDIO_CAPTURE_I2S
  i2s_chan_config_t channel_config = I2S_CHANNEL_DEFAULT_CONFIG(I2S_NUM_AUTO, I2S_ROLE_MASTER);
  if (i2s_new_channel(&channel_config, nullptr, &rx_channel) != ESP_OK) {
    rx_channel = nullptr;
    Serial.println("audio i2s channel initialization failed");
    return;
  }

  i2s_std_config_t standard_config = {};
  standard_config.clk_cfg = I2S_STD_CLK_DEFAULT_CONFIG(AUDIO_SAMPLE_RATE);
  standard_config.slot_cfg = I2S_STD_PHILIPS_SLOT_DEFAULT_CONFIG(
      I2S_DATA_BIT_WIDTH_16BIT, I2S_SLOT_MODE_MONO);
  standard_config.gpio_cfg.mclk = I2S_GPIO_UNUSED;
  standard_config.gpio_cfg.bclk = static_cast<gpio_num_t>(AUDIO_I2S_BCLK_PIN);
  standard_config.gpio_cfg.ws = static_cast<gpio_num_t>(AUDIO_I2S_WS_PIN);
  standard_config.gpio_cfg.dout = I2S_GPIO_UNUSED;
  standard_config.gpio_cfg.din = static_cast<gpio_num_t>(AUDIO_I2S_DATA_PIN);
  standard_config.gpio_cfg.invert_flags.mclk_inv = false;
  standard_config.gpio_cfg.invert_flags.bclk_inv = false;
  standard_config.gpio_cfg.invert_flags.ws_inv = false;
  if (i2s_channel_init_std_mode(rx_channel, &standard_config) != ESP_OK ||
      i2s_channel_enable(rx_channel) != ESP_OK) {
    i2s_del_channel(rx_channel);
    rx_channel = nullptr;
    Serial.println("audio i2s standard mode initialization failed");
    return;
  }
  audio_server.begin();
  audio_server.setNoDelay(true);
  Serial.println("audio capture ready");
#else
  Serial.println("audio capture disabled");
#endif
}

void audioLoop() {
#if AUDIO_CAPTURE_BACKEND == AUDIO_CAPTURE_I2S
  if (!rx_channel) return;
  WiFiClient incoming = audio_server.accept();
  if (incoming) {
    if (!stream_requested || audio_client.connected()) {
      incoming.stop();
    } else {
      audio_client = incoming;
      audio_client.setNoDelay(true);
      audio_client.printf("PCM %d %d %d LE\n", AUDIO_SAMPLE_RATE, AUDIO_BITS, AUDIO_CHANNELS);
      ring_head = ring_tail = ring_used = 0;
      Serial.println("audio client connected");
    }
  }
  if (audio_client && !audio_client.connected()) {
    audio_client.stop();
    stream_requested = false;
    ring_head = ring_tail = ring_used = 0;
    Serial.println("audio client disconnected");
  }
  if (stream_requested && audio_client.connected()) {
    captureAudio();
    sendAudio();
  }
#endif
}

bool audioAvailable() {
#if AUDIO_CAPTURE_BACKEND == AUDIO_CAPTURE_I2S
  return rx_channel != nullptr;
#else
  return false;
#endif
}

bool audioSetStreaming(bool enabled) {
  if (enabled && !audioAvailable()) return false;
  stream_requested = enabled;
  if (!enabled && audio_client) {
    audio_client.stop();
  }
  return true;
}

bool audioIsStreaming() { return stream_requested && audio_client.connected(); }
