#include "network.h"

#include <WiFi.h>
#include <cstdlib>
#include <ctime>

#include "audio.h"
#include "battery.h"
#include "display.h"
#include "radio.h"

#if __has_include("config.h")
#include "config.h"
#else
#include "config.example.h"
#endif

namespace {
constexpr uint32_t RECONNECT_INTERVAL_MS = 10000;
constexpr time_t MIN_VALID_TIME = 1609459200;

WiFiServer control_server(CONTROL_PORT, 1);
WiFiClient control_client;
bool control_started = false;
bool hello_received = false;
char line_buffer[CONTROL_LINE_MAX + 1];
size_t line_length = 0;
bool line_overflow = false;
uint32_t last_connect_attempt_ms = 0;
char ip_text[16] = "";

void sendError(const char* reason) {
  control_client.print("ERR ");
  control_client.println(reason);
}

void sendStatus() {
  const RadioState& radio = radioGetState();
  const BatteryState& battery = batteryGetState();
  char response[384];
  snprintf(response, sizeof(response),
           "{\"state\":\"%s\",\"frequency_hz\":%llu,\"mode\":\"%s\","
           "\"volume\":%u,\"rssi\":%u,\"snr\":%u,\"battery_mv\":%u,"
           "\"external_power\":%s,\"wifi_rssi_dbm\":%ld,\"ip\":\"%s\","
           "\"time_synced\":%s,\"streaming\":%s}",
           radio.receiving ? "receiving" : "waiting",
           static_cast<unsigned long long>(radio.frequency_hz),
           radioModeName(radio.mode), radio.volume, radio.rssi, radio.snr,
           battery.voltage_mv, battery.external_power ? "true" : "false",
           static_cast<long>(networkRSSIDBm()), networkIPAddress(),
           networkTimeSynced() ? "true" : "false",
           audioIsStreaming() ? "true" : "false");
  control_client.println(response);
}

bool noExtraToken(const char* line, int consumed) {
  while (line[consumed] == ' ') ++consumed;
  return line[consumed] == '\0';
}

void processCommand(char* line) {
  if (!strcmp(line, "HELLO 1")) {
    hello_received = true;
    control_client.println("OK");
    return;
  }
  if (!hello_received) {
    sendError("handshake-required");
    return;
  }
  if (!strcmp(line, "PING")) {
    control_client.println("OK");
  } else if (!strcmp(line, "STATUS")) {
    sendStatus();
  } else if (!strncmp(line, "TUNE ", 5)) {
    char mode_text[4] = {};
    unsigned long long frequency_hz = 0;
    int consumed = 0;
    if (sscanf(line, "TUNE %3s %llu%n", mode_text, &frequency_hz, &consumed) != 2 ||
        !noExtraToken(line, consumed)) {
      sendError("malformed-command");
      return;
    }
    RadioMode mode;
    if (!radioParseMode(mode_text, &mode)) {
      sendError("invalid-mode");
    } else if (!radioFrequencyValid(mode, frequency_hz)) {
      sendError("invalid-frequency");
    } else if (!radioTune(mode, frequency_hz)) {
      sendError("radio-error");
    } else {
      control_client.println("OK");
      displayRequestRedraw();
    }
  } else if (!strncmp(line, "VOLUME ", 7)) {
    unsigned int volume = 0;
    int consumed = 0;
    if (sscanf(line, "VOLUME %u%n", &volume, &consumed) != 1 ||
        !noExtraToken(line, consumed)) {
      sendError("malformed-command");
    } else if (volume > 63) {
      sendError("invalid-volume");
    } else if (!radioSetVolume(static_cast<uint8_t>(volume))) {
      sendError("radio-error");
    } else {
      control_client.println("OK");
      displayRequestRedraw();
    }
  } else if (!strcmp(line, "STREAM START")) {
    if (!audioSetStreaming(true)) sendError("audio-unavailable");
    else control_client.println("OK");
  } else if (!strcmp(line, "STREAM STOP")) {
    audioSetStreaming(false);
    control_client.println("OK");
  } else {
    sendError("unsupported-command");
  }
}

void startWiFi() {
  last_connect_attempt_ms = millis();
  WiFi.mode(WIFI_STA);
  WiFi.setHostname(WIFI_HOSTNAME);
  WiFi.setAutoReconnect(true);
  WiFi.persistent(false);
  if (!WIFI_USE_DHCP) {
    IPAddress ip, gateway, netmask, dns;
    if (!ip.fromString(WIFI_STATIC_IP) || !gateway.fromString(WIFI_GATEWAY) ||
        !netmask.fromString(WIFI_NETMASK) || !dns.fromString(WIFI_DNS) ||
        !WiFi.config(ip, gateway, netmask, dns)) {
      Serial.println("static IP configuration failed");
    }
  }
  if (WIFI_SSID[0]) {
    WiFi.begin(WIFI_SSID, WIFI_PASSWORD);
    Serial.println("wifi connection requested");
  } else {
    Serial.println("wifi credentials are empty");
  }
}
}  // namespace

void networkBegin() {
  setenv("TZ", DISPLAY_TIMEZONE, 1);
  tzset();
  configTime(0, 0, NTP_SERVER);
  startWiFi();
}

void networkLoop() {
  if (WiFi.status() != WL_CONNECTED) {
    if (control_client) control_client.stop();
    hello_received = false;
    if (control_started) {
      control_server.end();
      control_started = false;
    }
    ip_text[0] = '\0';
    if (WIFI_SSID[0] && millis() - last_connect_attempt_ms >= RECONNECT_INTERVAL_MS) {
      Serial.println("wifi reconnecting");
      WiFi.disconnect();
      WiFi.begin(WIFI_SSID, WIFI_PASSWORD);
      last_connect_attempt_ms = millis();
    }
    return;
  }

  WiFi.localIP().toString().toCharArray(ip_text, sizeof(ip_text));
  if (!control_started) {
    control_server.begin();
    control_server.setNoDelay(true);
    control_started = true;
    Serial.print("wifi connected ip=");
    Serial.println(ip_text);
  }

  WiFiClient incoming = control_server.accept();
  if (incoming) {
    if (control_client.connected()) {
      incoming.stop();
    } else {
      control_client = incoming;
      control_client.setNoDelay(true);
      hello_received = false;
      line_length = 0;
      line_overflow = false;
      Serial.println("control client connected");
    }
  }
  if (control_client && !control_client.connected()) {
    control_client.stop();
    hello_received = false;
    line_length = 0;
    Serial.println("control client disconnected");
    return;
  }

  while (control_client.available()) {
    const char value = static_cast<char>(control_client.read());
    if (value == '\r') continue;
    if (value == '\n') {
      if (line_overflow) sendError("line-too-long");
      else {
        line_buffer[line_length] = '\0';
        if (line_length) processCommand(line_buffer);
      }
      line_length = 0;
      line_overflow = false;
    } else if (!line_overflow) {
      if (line_length < CONTROL_LINE_MAX) line_buffer[line_length++] = value;
      else line_overflow = true;
    }
  }
}

bool networkConnected() { return WiFi.status() == WL_CONNECTED; }

bool networkTimeSynced() { return time(nullptr) >= MIN_VALID_TIME; }

int32_t networkRSSIDBm() { return networkConnected() ? WiFi.RSSI() : 0; }

const char* networkIPAddress() { return ip_text; }

const char* networkSSID() { return networkConnected() ? WIFI_SSID : ""; }
