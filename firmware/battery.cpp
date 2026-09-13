#include "battery.h"

#if __has_include("config.h")
#include "config.h"
#else
#include "config.example.h"
#endif

namespace {
constexpr uint32_t POLL_INTERVAL_MS = 2000;
constexpr uint16_t LEVEL_25_MV = 3680;
constexpr uint16_t LEVEL_50_MV = 3780;
constexpr uint16_t LEVEL_75_MV = 3880;
constexpr uint16_t HYSTERESIS_MV = 20;

BatteryState state{4000, false, 0};
uint8_t level = 0;
uint32_t last_poll_ms = 0;

void updateLevel(uint16_t voltage_mv) {
  switch (level) {
    case 0:
      if (voltage_mv > LEVEL_25_MV + HYSTERESIS_MV) level = 1;
      break;
    case 1:
      if (voltage_mv > LEVEL_50_MV + HYSTERESIS_MV) level = 2;
      else if (voltage_mv < LEVEL_25_MV - HYSTERESIS_MV) level = 0;
      break;
    case 2:
      if (voltage_mv > LEVEL_75_MV + HYSTERESIS_MV) level = 3;
      else if (voltage_mv < LEVEL_50_MV - HYSTERESIS_MV) level = 1;
      break;
    case 3:
      if (voltage_mv < LEVEL_75_MV - HYSTERESIS_MV) level = 2;
      break;
    default:
      level = 0;
  }
  state.percentage = static_cast<uint8_t>((level + 1) * 25);
}
}  // namespace

void batteryBegin() {
  pinMode(BATTERY_ADC_PIN, INPUT);
  batteryPoll();
}

void batteryPoll() {
  if (last_poll_ms && millis() - last_poll_ms < POLL_INTERVAL_MS) return;
  last_poll_ms = millis();
  uint32_t total = 0;
  for (uint8_t i = 0; i < BATTERY_ADC_READS; ++i) total += analogRead(BATTERY_ADC_PIN);
  const float average = static_cast<float>(total) / BATTERY_ADC_READS;
  state.voltage_mv = static_cast<uint16_t>(average * BATTERY_ADC_FACTOR);
  state.external_power = state.voltage_mv > EXTERNAL_POWER_THRESHOLD_MV;
  if (!state.external_power) updateLevel(state.voltage_mv);
}

const BatteryState& batteryGetState() { return state; }
