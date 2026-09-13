# Audio hardware

Stock ATS-Mini hardware does not connect the SI4732 analog outputs to the
ESP32-S3. Audio streaming is disabled by default and `STREAM START` returns
`ERR audio-unavailable`.

Before fitting an ADC:

1. Identify the exact PCB revision and trace both SI4732 audio outputs.
2. Confirm GPIO11–14 are genuinely unconnected on that revision.
3. Take line-level audio before the NS4160/AD4150B power amplifier, not from
   the speaker outputs.
4. Use an external I²S ADC with appropriate AC coupling, input bias, level
   scaling, anti-alias filtering, and common ground.
5. Confirm 3.3 V logic compatibility and avoid loading the headphone path.
6. Verify BCLK, word-select, data polarity, channel slot, and sample format on
   a logic analyzer before enabling network streaming.

The default optional wiring is:

| ADC signal | ESP32-S3 |
|---|---:|
| BCLK | GPIO11 |
| WS/LRCLK | GPIO12 |
| DOUT | GPIO13 |
| Optional MCLK/control | GPIO14, only after verification |

Copy `firmware/config.example.h` to `firmware/config.h`, select
`AUDIO_CAPTURE_I2S`, and update the pin macros to match the verified wiring.
The initial protocol sends `PCM 16000 16 1 LE` followed by raw samples.

The built-in ADC is intentionally not implemented as a production backend. It
would still require a hardware audio tap and analog conditioning, and it is a
poor fit for predictable recording quality.
