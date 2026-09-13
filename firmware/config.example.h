#pragma once

// Copy this file to config.h and set local credentials. Never commit config.h.
#define WIFI_SSID ""
#define WIFI_PASSWORD ""
#define WIFI_USE_DHCP true
#define WIFI_STATIC_IP "192.168.1.42"
#define WIFI_GATEWAY "192.168.1.1"
#define WIFI_NETMASK "255.255.255.0"
#define WIFI_DNS "192.168.1.1"
#define WIFI_HOSTNAME "ats-radio"

#define NTP_SERVER "pool.ntp.org"
#define DISPLAY_TIMEZONE "UTC0"

#define CONTROL_PORT 60000
#define AUDIO_PORT 60001
#define CONTROL_LINE_MAX 192

#define DISPLAY_IDLE_MS 60000UL
#define DISPLAY_BRIGHTNESS 200
#define PIN_LCD_BL 38

// The original ATS-Mini board has no SI4732-to-ESP32 audio connection.
// Keep NONE unless an external I2S ADC has been physically installed.
#define AUDIO_CAPTURE_NONE 0
#define AUDIO_CAPTURE_I2S 1
#ifndef AUDIO_CAPTURE_BACKEND
#define AUDIO_CAPTURE_BACKEND AUDIO_CAPTURE_NONE
#endif
#define AUDIO_SAMPLE_RATE 16000
#define AUDIO_BITS 16
#define AUDIO_CHANNELS 1
#define AUDIO_I2S_BCLK_PIN 11
#define AUDIO_I2S_WS_PIN 12
#define AUDIO_I2S_DATA_PIN 13

#define BATTERY_ADC_PIN 4
#define BATTERY_ADC_READS 10
#define BATTERY_ADC_FACTOR 1.702f
#define EXTERNAL_POWER_THRESHOLD_MV 4300
