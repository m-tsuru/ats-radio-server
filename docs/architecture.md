# Architecture

## System boundaries

`radioctl` talks only to `radiod` over a Unix domain socket. `radiod` is the
single owner of the receiver and uses TCP port 60000 for control and port 60001
for audio. The firmware performs receiver, network, time, battery, display, and
optional PCM capture duties. Scheduling, files, codecs, and playback remain on
the Linux host.

```text
radioctl -- Unix socket --> radiod -- TCP 60000 --> ESP32-S3/SI4732
                              |     -- TCP 60001 --> raw PCM (when fitted)
                              +---- ffmpeg ------> FLAC/WAV
```

The firmware starts and remains useful with `AUDIO_CAPTURE_NONE`: radio,
Wi-Fi, NTP, remote control, quality readings, battery monitoring, and the
display do not depend on audio capture.

## Phase 0 hardware feasibility

### Target board and evidence

The firmware target is the original ESP32-S3-WROOM-1 + SI4732-A10 ATS-Mini
layout used by the current
[`ats-nano`](https://github.com/esp32-si4732/ats-nano) pin definitions. Hardware
was checked against the published schematic linked from the
[`ats-mini` hardware notes](https://github.com/esp32-si4732/ats-mini/blob/main/docs/source/hardware.md).
The repository warns that other currently sold PCB revisions do not have
published schematics. Those revisions are therefore unverified and must be
checked physically before enabling capture.

### Pinout

| Function | ESP32-S3 GPIO | Finding |
|---|---:|---|
| Battery monitor | 4 | Connected through a 100k/100k divider |
| LCD reset/CS/DC/WR/RD | 5/6/7/8/9 | In use |
| Audio amplifier enable | 10 | In use |
| Spare | 11/12/13/14 | Marked NC/spare on the published board |
| Radio power/reset | 15/16 | In use |
| SI4732 I2C clock/data | 17/18 | In use |
| USB D-/D+ | 19/20 | In use for native USB |
| Encoder switch/A/B | 21/2/1 | In use |
| LCD backlight/data bus | 38/39/40/41/42/45/46/47/48 | In use |
| OSPI PSRAM | 35/36/37 | Not available as general-purpose pins |

GPIO11, GPIO12, and GPIO13 are the default BCLK, word-select, and data-input
pins for an optional external I²S ADC. GPIO14 remains spare. These assignments
must be confirmed on the exact PCB before soldering because later revisions
may differ.

### Audio path and capture decision

On the published schematic, SI4732 `LOUT/DFS` and `ROUT/DOUT` operate as analog
left/right outputs and run to the headphone network and NS4160/AD4150B power
amplifier. Neither signal is connected to an ESP32-S3 ADC or I²S input. The
ESP32-S3 therefore cannot capture stock-board audio through firmware alone.

The ESP32-S3 ADC is not selected for production recording. It would require
analog tapping, biasing, attenuation, anti-alias filtering, and careful
grounding, while offering lower and less predictable audio quality in this RF
device. It remains an experimental fallback only.

The recommended modification is a stereo-capable external I²S ADC connected
to the line-level L/R nodes before the speaker power amplifier. The initial
transport profile is 16 kHz, signed 16-bit, mono, little-endian PCM. An ADC and
front end should support at least 16-bit conversion; 24-bit hardware is useful
for headroom even when the transmitted initial profile is 16-bit mono.

No specific ADC module can be guaranteed without its input-range, clocking,
and analog-front-end schematic. A PCM1808-class or equivalent I²S ADC is a
reasonable engineering candidate, but it is not assumed by the firmware. The
module must provide safe line-level input conditioning and 3.3 V-compatible
digital I/O.

### Build-time backends

- `AUDIO_CAPTURE_NONE` is the default and requires no hardware modification.
- `AUDIO_CAPTURE_I2S` enables an external-ADC receive channel, an 8 KiB ring
  buffer, and TCP port 60001.

Set the backend and verified pins only in `firmware/config.h`. Capture must not
be enabled until the PCB revision and wiring have been inspected.

## Runtime ownership

`radiod` serializes all device use with four states: `idle`, `listening`,
`manual_recording`, and `scheduled_recording`. Tune and volume operations are
rejected while a non-idle owner is active. Overlapping enabled reservations
are rejected when loaded, added, or enabled. A failed stream is closed and
ffmpeg receives EOF so that it can finalize and retain any partial output.

The scheduler uses the configured IANA timezone. Monthly dates that do not
exist are skipped rather than moved to the end of the month. The firmware's
epoch clock remains UTC; the configured POSIX timezone is applied only when
formatting the display.
