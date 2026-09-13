# Protocols

Both device protocols are intended for trusted LAN use. They have no
authentication and must not be exposed to an untrusted network.

## Device control: TCP 60000

Requests and responses are ASCII/UTF-8 lines terminated by LF (CRLF is also
accepted). A client must begin with `HELLO 1`. The firmware accepts one control
client and bounds each input line to 192 bytes by default.

| Request | Response |
|---|---|
| `HELLO 1` | `OK` |
| `PING` | `OK` |
| `STATUS` | One JSON object line |
| `TUNE MODE FREQUENCY_HZ` | `OK` or `ERR reason` |
| `VOLUME 0..63` | `OK` or `ERR invalid-volume` |
| `STREAM START` | `OK` or `ERR audio-unavailable` |
| `STREAM STOP` | `OK` |

Modes are `FM`, `AM`, `USB`, and `LSB`. Every external frequency is an integer
number of Hz. FM accepts 64–108 MHz in 10 kHz increments; AM accepts 150 kHz–30
MHz in 1 kHz increments; USB/LSB accept 1.7–30 MHz in 1 kHz increments.

Example status:

```json
{"state":"receiving","frequency_hz":80000000,"mode":"FM","volume":35,"rssi":42,"snr":27,"battery_mv":3910,"external_power":false,"wifi_rssi_dbm":-54,"ip":"192.168.1.42","time_synced":true,"streaming":false}
```

`rssi` and `snr` are SI4732 receiver-quality values. `wifi_rssi_dbm` is the
separate Wi-Fi signal value. `external_power` is a voltage-based estimate, not
a claim that the battery is charging.

## Device audio: TCP 60001

After a successful `STREAM START`, connect to port 60001. The device sends one
header line followed immediately by raw PCM bytes:

```text
PCM 16000 16 1 LE
<signed little-endian samples...>
```

Only one audio client is accepted. Disconnecting the client clears the stream
request and leaves the receiver/control service running.

## Local daemon IPC

`radioctl` sends one bounded JSON request over the configured Unix socket.
`radiod` replies with one JSON line. The `listen` reply is followed by raw PCM
on the same Unix connection. Socket mode is `0660`.

Commands are `status`, `tune`, `volume`, `listen`, `record.start`,
`record.stop`, `schedule.list`, `schedule.add`, `schedule.remove`,
`schedule.enable`, and `schedule.disable`.
