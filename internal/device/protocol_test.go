package device

import "testing"

func TestParseStatusLine(t *testing.T) {
	line := `{"state":"receiving","frequency_hz":80000000,"mode":"FM","volume":35,"rssi":42,"snr":27,"battery_mv":3910,"external_power":false,"wifi_rssi_dbm":-54,"ip":"192.0.2.5","time_synced":true,"streaming":false}`
	status, err := ParseStatusLine(line)
	if err != nil {
		t.Fatal(err)
	}
	if status.FrequencyHz != 80_000_000 || status.Mode != ModeFM || status.RSSI != 42 {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestParseStatusLineRejectsInternalFrequencyUnits(t *testing.T) {
	line := `{"state":"receiving","frequency_hz":8000,"mode":"FM","volume":35}`
	if _, err := ParseStatusLine(line); err == nil {
		t.Fatal("expected frequency validation error")
	}
}

func TestParseProtocolError(t *testing.T) {
	err := ParseResponseLine("ERR invalid-frequency\r\n")
	protocolErr, ok := err.(*ProtocolError)
	if !ok || protocolErr.Reason != "invalid-frequency" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParsePCMHeader(t *testing.T) {
	header, err := ParsePCMHeader("PCM 16000 16 1 LE\n")
	if err != nil {
		t.Fatal(err)
	}
	if header.SampleRate != 16000 || header.Bits != 16 || header.Channels != 1 {
		t.Fatalf("unexpected header: %+v", header)
	}
}
