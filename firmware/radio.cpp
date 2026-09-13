#include "radio.h"

#include <Wire.h>
#include "../SI4735-fixed.h"
#include "../patch_init.h"

namespace {
constexpr int PIN_POWER_ON = 15;
constexpr int RESET_PIN = 16;
constexpr int PIN_AMP_EN = 10;
constexpr int ESP32_I2C_SCL = 17;
constexpr int ESP32_I2C_SDA = 18;
constexpr uint32_t QUALITY_INTERVAL_MS = 1000;

SI4735_fixed receiver;
RadioState state{80000000ULL, RadioMode::FM, 35, 0, 0, false};
uint32_t last_quality_ms = 0;
bool radio_ready = false;
bool receiver_awake = false;

bool applyTuning() {
  digitalWrite(PIN_AMP_EN, LOW);
  if (!receiver_awake) {
    receiver.setup(RESET_PIN, 0);
    receiver_awake = true;
  }

  const uint16_t frequency = state.mode == RadioMode::FM
      ? static_cast<uint16_t>(state.frequency_hz / 10000ULL)
      : static_cast<uint16_t>(state.frequency_hz / 1000ULL);

  switch (state.mode) {
    case RadioMode::FM:
      receiver.setFM(6400, 10800, frequency, 10);
      break;
    case RadioMode::AM:
      receiver.setAM(150, 30000, frequency, 1);
      break;
    case RadioMode::USB:
    case RadioMode::LSB:
      receiver.loadPatch(ssb_patch_content, sizeof(ssb_patch_content));
      receiver.setSSB(1700, 30000, frequency, 1,
                      state.mode == RadioMode::LSB ? 1 : 2);
      receiver.setSSBBfo(0);
      break;
  }
  receiver.setVolume(state.volume);
  // The receiver output remains available for an optional capture front end,
  // but the on-device speaker amplifier must never be enabled.
  digitalWrite(PIN_AMP_EN, LOW);
  state.receiving = true;
  return true;
}
}  // namespace

bool radioBegin() {
  pinMode(PIN_POWER_ON, OUTPUT);
  pinMode(PIN_AMP_EN, OUTPUT);
  digitalWrite(PIN_POWER_ON, HIGH);
  digitalWrite(PIN_AMP_EN, LOW);
  Wire.begin(ESP32_I2C_SDA, ESP32_I2C_SCL);
  if (!receiver.getDeviceI2CAddress(RESET_PIN)) {
    Serial.println("radio not found");
    state.receiving = false;
    return false;
  }
  radio_ready = true;
  receiver_awake = false;
  state.receiving = false;
  Serial.println("radio ready; waiting for remote tune");
  return true;
}

bool radioTune(RadioMode mode, uint64_t frequency_hz) {
  if (!radio_ready || !radioFrequencyValid(mode, frequency_hz)) return false;
  state.mode = mode;
  state.frequency_hz = frequency_hz;
  return applyTuning();
}

bool radioSetVolume(uint8_t volume) {
  if (!radio_ready || volume > 63) return false;
  state.volume = volume;
  if (receiver_awake) receiver.setVolume(volume);
  return true;
}

void radioPoll() {
  if (!state.receiving || millis() - last_quality_ms < QUALITY_INTERVAL_MS) return;
  last_quality_ms = millis();
  receiver.getCurrentReceivedSignalQuality();
  state.rssi = receiver.getCurrentRSSI();
  state.snr = receiver.getCurrentSNR();
}

const RadioState& radioGetState() { return state; }

const char* radioModeName(RadioMode mode) {
  switch (mode) {
    case RadioMode::FM: return "FM";
    case RadioMode::AM: return "AM";
    case RadioMode::USB: return "USB";
    case RadioMode::LSB: return "LSB";
  }
  return "AM";
}

bool radioParseMode(const char* text, RadioMode* mode) {
  if (!strcmp(text, "FM")) *mode = RadioMode::FM;
  else if (!strcmp(text, "AM")) *mode = RadioMode::AM;
  else if (!strcmp(text, "USB")) *mode = RadioMode::USB;
  else if (!strcmp(text, "LSB")) *mode = RadioMode::LSB;
  else return false;
  return true;
}

bool radioFrequencyValid(RadioMode mode, uint64_t frequency_hz) {
  switch (mode) {
    case RadioMode::FM:
      return frequency_hz >= 64000000ULL && frequency_hz <= 108000000ULL &&
             frequency_hz % 10000ULL == 0;
    case RadioMode::AM:
      return frequency_hz >= 150000ULL && frequency_hz <= 30000000ULL &&
             frequency_hz % 1000ULL == 0;
    case RadioMode::USB:
    case RadioMode::LSB:
      return frequency_hz >= 1700000ULL && frequency_hz <= 30000000ULL &&
             frequency_hz % 1000ULL == 0;
  }
  return false;
}
