#include "display.h"

#include <Arduino.h>
#include <TFT_eSPI.h>
#include <ctime>

#include "battery.h"
#include "network.h"
#include "radio.h"

#if __has_include("config.h")
#include "config.h"
#else
#include "config.example.h"
#endif

namespace {
constexpr int SCREEN_WIDTH = 320;
constexpr int SCREEN_HEIGHT = 170;
constexpr uint32_t REFRESH_INTERVAL_MS = 1000;
constexpr uint16_t COLOR_BACKGROUND = 0x0862;
constexpr uint16_t COLOR_PANEL = 0x10C4;
constexpr uint16_t COLOR_PANEL_LIGHT = 0x1946;
constexpr uint16_t COLOR_PRIMARY = 0xFFFF;
constexpr uint16_t COLOR_MUTED = 0x9D36;
constexpr uint16_t COLOR_ACCENT = 0x07FF;
constexpr uint16_t COLOR_GOOD = 0x07E0;
constexpr uint16_t COLOR_WARN = 0xFD20;
constexpr uint16_t COLOR_DANGER = 0xF940;

TFT_eSPI tft;
TFT_eSprite sprite(&tft);
bool sleeping = false;
bool redraw_requested = true;
uint32_t last_activity_ms = 0;
uint32_t last_refresh_ms = 0;

void setSleeping(bool enabled) {
  if (enabled == sleeping) return;
  sleeping = enabled;
  if (enabled) {
    ledcWrite(PIN_LCD_BL, 0);
    tft.writecommand(0x28);  // ST7789 DISPOFF; CPU and network remain active.
  } else {
    tft.writecommand(0x29);  // ST7789 DISPON.
    ledcWrite(PIN_LCD_BL, DISPLAY_BRIGHTNESS);
    redraw_requested = true;
  }
}

void drawStatus() {
  const RadioState& radio = radioGetState();
  const BatteryState& battery = batteryGetState();
  char frequency_text[20];
  char clock_text[6] = "--:--";

  if (networkTimeSynced()) {
    const time_t now = time(nullptr);
    tm local_time;
    localtime_r(&now, &local_time);
    snprintf(clock_text, sizeof(clock_text), "%02d:%02d", local_time.tm_hour, local_time.tm_min);
  }

  sprite.fillSprite(COLOR_BACKGROUND);
  sprite.fillRoundRect(6, 5, 308, 25, 7, COLOR_PANEL);

  // Network indicator and identity.
  const bool connected = networkConnected();
  const int32_t wifi_rssi = networkRSSIDBm();
  const uint8_t wifi_bars = !connected ? 0
      : (wifi_rssi >= -55 ? 4 : (wifi_rssi >= -65 ? 3 : (wifi_rssi >= -75 ? 2 : 1)));
  for (uint8_t i = 0; i < 4; ++i) {
    const int height = 3 + i * 3;
    sprite.fillRect(14 + i * 5, 23 - height, 3, height,
                    i < wifi_bars ? COLOR_ACCENT : COLOR_PANEL_LIGHT);
  }
  sprite.setTextDatum(TL_DATUM);
  sprite.setTextFont(2);
  sprite.setTextColor(COLOR_PRIMARY, COLOR_PANEL);
  sprite.drawString(connected ? networkSSID() : "OFFLINE", 40, 10);
  sprite.setTextColor(COLOR_MUTED, COLOR_PANEL);
  sprite.drawString("REMOTE RX", 137, 10);
  sprite.setTextDatum(TR_DATUM);
  sprite.setTextColor(COLOR_PRIMARY, COLOR_PANEL);
  sprite.drawString(clock_text, 306, 10);

  if (!radio.receiving) {
    sprite.fillRoundRect(20, 43, 280, 70, 10, COLOR_PANEL);
    sprite.drawRoundRect(20, 43, 280, 70, 10, COLOR_PANEL_LIGHT);
    sprite.setTextDatum(TC_DATUM);
    sprite.setTextFont(4);
    sprite.setTextColor(COLOR_ACCENT, COLOR_PANEL);
    sprite.drawString("REMOTE STANDBY", 160, 55);
    sprite.setTextFont(2);
    sprite.setTextColor(COLOR_MUTED, COLOR_PANEL);
    sprite.drawString("Send TUNE to start receiving", 160, 87);
  } else {
    if (radio.mode == RadioMode::FM) {
      snprintf(frequency_text, sizeof(frequency_text), "%.2f",
               static_cast<double>(radio.frequency_hz) / 1000000.0);
    } else {
      snprintf(frequency_text, sizeof(frequency_text), "%llu",
               static_cast<unsigned long long>(radio.frequency_hz / 1000ULL));
    }

    sprite.fillRoundRect(12, 42, 51, 25, 6, COLOR_ACCENT);
    sprite.setTextDatum(MC_DATUM);
    sprite.setTextFont(2);
    sprite.setTextColor(COLOR_BACKGROUND, COLOR_ACCENT);
    sprite.drawString(radioModeName(radio.mode), 37, 54);

    sprite.setTextDatum(TC_DATUM);
    sprite.setTextFont(7);
    sprite.setTextColor(COLOR_PRIMARY, COLOR_BACKGROUND);
    sprite.drawString(frequency_text, 169, 41);
    sprite.setTextDatum(TR_DATUM);
    sprite.setTextFont(2);
    sprite.setTextColor(COLOR_MUTED, COLOR_BACKGROUND);
    sprite.drawString(radio.mode == RadioMode::FM ? "MHz" : "kHz", 307, 91);
  }

  // Signal meter, SNR, silent-speaker state, and battery live in one footer.
  sprite.fillRoundRect(6, 120, 308, 44, 7, COLOR_PANEL);
  sprite.setTextDatum(TL_DATUM);
  sprite.setTextFont(2);
  sprite.setTextColor(COLOR_MUTED, COLOR_PANEL);
  sprite.drawString("S", 14, 126);
  const uint8_t signal_segments = radio.receiving
      ? static_cast<uint8_t>((radio.rssi > 90 ? 90 : radio.rssi) * 12 / 90)
      : 0;
  for (uint8_t i = 0; i < 12; ++i) {
    const uint16_t active_color = i < 9 ? COLOR_GOOD : COLOR_WARN;
    sprite.fillRoundRect(31 + i * 10, 129, 7, 8, 2,
                         i < signal_segments ? active_color : COLOR_PANEL_LIGHT);
  }
  sprite.setTextColor(COLOR_PRIMARY, COLOR_PANEL);
  char quality_text[32];
  if (radio.receiving) {
    snprintf(quality_text, sizeof(quality_text), "RSSI %u  SNR %u", radio.rssi, radio.snr);
  } else {
    snprintf(quality_text, sizeof(quality_text), "RSSI --  SNR --");
  }
  sprite.drawString(quality_text, 159, 126);

  sprite.setTextColor(COLOR_DANGER, COLOR_PANEL);
  sprite.drawString("SPK OFF", 14, 146);
  sprite.setTextColor(COLOR_MUTED, COLOR_PANEL);
  sprite.drawString(connected ? networkIPAddress() : "waiting for Wi-Fi", 90, 146);

  const int battery_x = 273;
  sprite.drawRoundRect(battery_x, 145, 27, 13, 3, COLOR_MUTED);
  sprite.fillRect(battery_x + 27, 149, 3, 5, COLOR_MUTED);
  const uint8_t battery_width = static_cast<uint8_t>(battery.percentage * 23 / 100);
  const uint16_t battery_color = battery.percentage <= 25 ? COLOR_DANGER : COLOR_GOOD;
  if (battery.external_power) {
    sprite.fillRect(battery_x + 3, 148, 4, 7, COLOR_WARN);
    sprite.fillTriangle(battery_x + 7, 148, battery_x + 7, 155,
                        battery_x + 14, 151, COLOR_WARN);
  } else if (battery_width) {
    sprite.fillRoundRect(battery_x + 2, 147, battery_width, 9, 2, battery_color);
  }
  sprite.pushSprite(0, 0);
}
}  // namespace

void displayBegin() {
  tft.begin();
  tft.setRotation(3);
  sprite.createSprite(SCREEN_WIDTH, SCREEN_HEIGHT);
  ledcAttach(PIN_LCD_BL, 16000, 8);
  ledcWrite(PIN_LCD_BL, DISPLAY_BRIGHTNESS);
  last_activity_ms = millis();
  drawStatus();
}

void displayLoop() {
  const uint32_t now_ms = millis();
  if (!sleeping && DISPLAY_IDLE_MS && now_ms - last_activity_ms >= DISPLAY_IDLE_MS) {
    setSleeping(true);
  }
  if (sleeping) return;
  if (redraw_requested || now_ms - last_refresh_ms >= REFRESH_INTERVAL_MS) {
    drawStatus();
    redraw_requested = false;
    last_refresh_ms = now_ms;
  }
}

void displayRequestRedraw() { redraw_requested = true; }

void displayUserActivity() {
  last_activity_ms = millis();
  if (sleeping) setSleeping(false);
}

bool displayIsSleeping() { return sleeping; }
