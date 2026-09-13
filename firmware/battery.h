#pragma once

#include <Arduino.h>

struct BatteryState {
  uint16_t voltage_mv;
  bool external_power;
  uint8_t percentage;
};

void batteryBegin();
void batteryPoll();
const BatteryState& batteryGetState();
