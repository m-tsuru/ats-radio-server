#pragma once

#include <Arduino.h>

enum class RadioMode : uint8_t { FM, AM, USB, LSB };

struct RadioState {
  uint64_t frequency_hz;
  RadioMode mode;
  uint8_t volume;
  uint8_t rssi;
  uint8_t snr;
  bool receiving;
};

bool radioBegin();
bool radioTune(RadioMode mode, uint64_t frequency_hz);
bool radioSetVolume(uint8_t volume);
void radioPoll();
const RadioState& radioGetState();
const char* radioModeName(RadioMode mode);
bool radioParseMode(const char* text, RadioMode* mode);
bool radioFrequencyValid(RadioMode mode, uint64_t frequency_hz);
