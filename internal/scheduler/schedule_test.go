package scheduler

import (
	"testing"
	"time"

	"github.com/m-tsuru/ats-radio-server/internal/device"
)

func baseReservation(kind Type) Reservation {
	return Reservation{
		ID:      "test",
		Enabled: true,
		Station: Station{FrequencyHz: 80_000_000, Mode: device.ModeFM},
		Schedule: Rule{
			Type: kind,
			Time: "07:00",
		},
		DurationSeconds: 1800,
		Output:          Output{Format: "flac"},
	}
}

func TestParseScheduleJSON(t *testing.T) {
	data := []byte(`[{"id":"news","enabled":true,"station":{"frequency_hz":80000000,"mode":"FM"},"schedule":{"type":"daily","time":"07:00"},"duration_seconds":1800,"output":{"format":"flac"}}]`)
	reservations, err := Parse(data, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if len(reservations) != 1 || reservations[0].ID != "news" {
		t.Fatalf("unexpected schedules: %+v", reservations)
	}
}

func TestParseScheduleJSONRejectsTrailingValue(t *testing.T) {
	data := []byte(`[] {}`)
	if _, err := Parse(data, time.UTC); err == nil {
		t.Fatal("expected trailing JSON error")
	}
}

func TestDailyNext(t *testing.T) {
	r := baseReservation(Daily)
	after := time.Date(2026, 9, 13, 6, 59, 0, 0, time.UTC)
	next, ok := r.Next(after, time.UTC)
	if !ok || !next.Equal(time.Date(2026, 9, 13, 7, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected next occurrence: %v, %v", next, ok)
	}
}

func TestWeeklyNext(t *testing.T) {
	r := baseReservation(Weekly)
	r.Schedule.Weekdays = []string{"mon", "fri"}
	after := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC) // Sunday.
	next, ok := r.Next(after, time.UTC)
	want := time.Date(2026, 9, 14, 7, 0, 0, 0, time.UTC)
	if !ok || !next.Equal(want) {
		t.Fatalf("got %v, want %v", next, want)
	}
}

func TestMonthlyNextSkipsNonexistentDate(t *testing.T) {
	r := baseReservation(Monthly)
	r.Schedule.Day = 31
	after := time.Date(2027, 1, 31, 8, 0, 0, 0, time.UTC)
	next, ok := r.Next(after, time.UTC)
	want := time.Date(2027, 3, 31, 7, 0, 0, 0, time.UTC)
	if !ok || !next.Equal(want) {
		t.Fatalf("got %v, want %v", next, want)
	}
}

func TestOneShotNext(t *testing.T) {
	r := baseReservation(OneShot)
	r.Schedule.Time = ""
	r.Schedule.At = "2026-09-13T07:00:00+09:00"
	after := time.Date(2026, 9, 12, 21, 0, 0, 0, time.UTC)
	next, ok := r.Next(after, time.UTC)
	want := time.Date(2026, 9, 12, 22, 0, 0, 0, time.UTC)
	if !ok || !next.Equal(want) {
		t.Fatalf("got %v, want %v", next, want)
	}
}

func TestOverlapDetection(t *testing.T) {
	a := baseReservation(Daily)
	a.ID = "a"
	a.Schedule.Time = "07:00"
	b := baseReservation(Weekly)
	b.ID = "b"
	b.Schedule.Time = "07:15"
	b.Schedule.Weekdays = []string{"mon"}
	left, right, ok := FindOverlap([]Reservation{a, b}, time.UTC)
	if !ok || left != "a" || right != "b" {
		t.Fatalf("expected overlap, got %q %q %v", left, right, ok)
	}
}

func TestAdjacentReservationsDoNotOverlap(t *testing.T) {
	a := baseReservation(Daily)
	a.ID = "a"
	a.Schedule.Time = "07:00"
	b := baseReservation(Daily)
	b.ID = "b"
	b.Schedule.Time = "07:30"
	if left, right, ok := FindOverlap([]Reservation{a, b}, time.UTC); ok {
		t.Fatalf("unexpected overlap %q %q", left, right)
	}
}
