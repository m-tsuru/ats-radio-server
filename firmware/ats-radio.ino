#include <Arduino.h>

#include "audio.h"
#include "battery.h"
#include "display.h"
#include "network.h"
#include "radio.h"

namespace {
constexpr int ENCODER_PIN_A = 2;
constexpr int ENCODER_PIN_B = 1;
constexpr int ENCODER_BUTTON_PIN = 21;
constexpr uint32_t BUTTON_DEBOUNCE_MS = 40;

volatile int8_t encoder_delta = 0;
volatile uint8_t encoder_previous = 0;
volatile int8_t encoder_quarters = 0;
bool button_previous = true;
uint32_t button_changed_ms = 0;

void ARDUINO_ISR_ATTR encoderInterrupt() {
  static const int8_t transitions[16] = {
      0, -1, 1, 0,
      1, 0, 0, -1,
      -1, 0, 0, 1,
      0, 1, -1, 0,
  };
  const uint8_t current = (digitalRead(ENCODER_PIN_A) << 1) | digitalRead(ENCODER_PIN_B);
  encoder_quarters += transitions[(encoder_previous << 2) | current];
  encoder_previous = current;
  if (encoder_quarters >= 4) {
    if (encoder_delta < 127) encoder_delta = static_cast<int8_t>(encoder_delta + 1);
    encoder_quarters = 0;
  } else if (encoder_quarters <= -4) {
    if (encoder_delta > -127) encoder_delta = static_cast<int8_t>(encoder_delta - 1);
    encoder_quarters = 0;
  }
}

void handleEncoder() {
  noInterrupts();
  const int8_t delta = encoder_delta;
  encoder_delta = 0;
  interrupts();
  if (!delta) return;
  displayUserActivity();

  const RadioState& state = radioGetState();
  if (!state.receiving) return;
  const int64_t step_hz = state.mode == RadioMode::FM ? 100000LL : 1000LL;
  int64_t next_hz = static_cast<int64_t>(state.frequency_hz) + delta * step_hz;
  const int64_t minimum_hz = state.mode == RadioMode::FM ? 64000000LL
      : (state.mode == RadioMode::AM ? 150000LL : 1700000LL);
  const int64_t maximum_hz = state.mode == RadioMode::FM ? 108000000LL : 30000000LL;
  if (next_hz > maximum_hz) next_hz = minimum_hz;
  if (next_hz < minimum_hz) next_hz = maximum_hz;
  if (radioTune(state.mode, static_cast<uint64_t>(next_hz))) displayRequestRedraw();
}

void handleButton() {
  const bool current = digitalRead(ENCODER_BUTTON_PIN) != LOW;
  if (current != button_previous && millis() - button_changed_ms >= BUTTON_DEBOUNCE_MS) {
    button_previous = current;
    button_changed_ms = millis();
    if (!current) displayUserActivity();
  }
}
}  // namespace

void setup() {
  Serial.begin(115200);
  pinMode(ENCODER_PIN_A, INPUT_PULLUP);
  pinMode(ENCODER_PIN_B, INPUT_PULLUP);
  pinMode(ENCODER_BUTTON_PIN, INPUT_PULLUP);
  encoder_previous = (digitalRead(ENCODER_PIN_A) << 1) | digitalRead(ENCODER_PIN_B);
  attachInterrupt(digitalPinToInterrupt(ENCODER_PIN_A), encoderInterrupt, CHANGE);
  attachInterrupt(digitalPinToInterrupt(ENCODER_PIN_B), encoderInterrupt, CHANGE);

  displayBegin();
  batteryBegin();
  radioBegin();
  networkBegin();
  audioBegin();
  displayRequestRedraw();
}

void loop() {
  handleEncoder();
  handleButton();
  radioPoll();
  batteryPoll();
  networkLoop();
  audioLoop();
  displayLoop();
  delay(2);
}
