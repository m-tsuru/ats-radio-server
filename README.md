# ATS Radio Server

A minimal network appliance based on
[`ats-nano`](https://github.com/esp32-si4732/ats-nano): an ESP32-S3/SI4732
receiver controlled by a Go daemon and CLI on a Linux host.

```text
radioctl -> /run/radiod/radiod.sock -> radiod -> ESP32 TCP 60000
                                           \-> audio TCP 60001 -> ffmpeg
```

This protocol is intended for trusted LAN use.

## Firmware

Requirements:

- Arduino CLI and the ESP32 Arduino core
- `TFT_eSPI`
- PU2CLR `SI4735`
- the original ATS-Mini board supported by `ats-nano`

Install the two firmware libraries and create the local configuration:

```sh
arduino-cli lib install "TFT_eSPI@2.5.43" "PU2CLR SI4735@2.1.8"
cp firmware/config.example.h firmware/config.h
```

Set the Wi-Fi credentials in `firmware/config.h`. That file is ignored by Git.
Select DHCP or static IPv4 and set the hostname, NTP server, and POSIX display
timezone there. The default `AUDIO_CAPTURE_NONE` build works without audio
capture hardware.

Compile and flash for an ESP32-S3 board whose Arduino partition/PSRAM options
match the installed module:

```sh
arduino-cli compile --warnings all --fqbn esp32:esp32:esp32s3 \
  --build-property "compiler.cpp.extra_flags=-DUSER_SETUP_LOADED -include $(pwd)/tft_setup.h" \
  firmware
arduino-cli upload -p /dev/ttyACM0 --fqbn esp32:esp32:esp32s3 firmware
```

The build property applies the board's checked-in parallel-display pinout to
both the sketch and TFT_eSPI itself; no global library file needs to be edited.

The actual port and FQBN options vary by host and board. See
[the audio hardware notes](docs/hardware-audio.md) before enabling I²S capture.

## Linux programs

Build and test without third-party Go dependencies:

```sh
go test ./...
go build -o bin/radiod ./cmd/radiod
go build -o bin/radioctl ./cmd/radioctl
```

Install the binaries, example configuration, and systemd unit:

```sh
sudo install -m 0755 bin/radiod bin/radioctl /usr/local/bin/
sudo install -d -m 0750 /etc/radiod
sudo install -m 0640 config.example.json /etc/radiod/config.json
sudo install -m 0644 packaging/radiod.service /etc/systemd/system/radiod.service
sudo useradd --system --home /var/lib/radiod --shell /usr/sbin/nologin radiod
sudo systemctl daemon-reload
sudo systemctl enable --now radiod
```

Adjust the config before starting. systemd creates `/run/radiod` and
`/var/lib/radiod`; logs go to the journal. SIGTERM closes active audio input,
allows ffmpeg to finalize its file, and shuts the Unix socket down.

## CLI

```sh
radioctl status
radioctl tune 80.0M
radioctl tune --mode USB 7.074M
radioctl volume 35
radioctl listen
radioctl record start
radioctl record start --format wav --mode AM 1000K
radioctl record stop
radioctl schedule list
radioctl schedule add schedule.json
radioctl schedule remove morning-news
radioctl schedule enable morning-news
radioctl schedule disable morning-news
```

`radioctl` always uses `radiod`; it never connects directly to the ESP32.
`listen` uses `ffplay` by default. Recording uses `ffmpeg` with separate
arguments (never a shell command string) and writes collision-free FLAC/WAV
filenames beneath `recording_dir`.

## Schedule JSON

`schedule add` accepts one object. The daemon persists an array at
`schedule_path` and rejects overlapping enabled reservations.

```json
{
  "id": "weekday-news",
  "enabled": true,
  "station": {"frequency_hz": 80000000, "mode": "FM"},
  "schedule": {
    "type": "weekly",
    "weekdays": ["mon", "tue", "wed", "thu", "fri"],
    "time": "07:00"
  },
  "duration_seconds": 1800,
  "output": {"format": "flac"}
}
```

Supported rules:

- `one-shot`: RFC3339 `at`, for example `2026-09-13T07:00:00+09:00`
- `daily`: local `time` in `HH:MM`
- `weekly`: local `time` plus `weekdays` (`sun` through `sat`)
- `monthly`: local `time` plus `day` (`1` through `31`)

A nonexistent monthly date is skipped. Times use the daemon config's IANA
timezone.

See [architecture](docs/architecture.md) and [protocols](docs/protocol.md) for
the hardware findings and complete interfaces.
