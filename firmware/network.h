#pragma once

#include <Arduino.h>

void networkBegin();
void networkLoop();
bool networkConnected();
bool networkTimeSynced();
int32_t networkRSSIDBm();
const char* networkIPAddress();
const char* networkSSID();
