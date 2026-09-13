package scheduler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/m-tsuru/ats-radio-server/internal/device"
)

type Type string

const (
	OneShot Type = "one-shot"
	Daily   Type = "daily"
	Weekly  Type = "weekly"
	Monthly Type = "monthly"
)

type Station struct {
	FrequencyHz uint64      `json:"frequency_hz"`
	Mode        device.Mode `json:"mode"`
}

type Rule struct {
	Type     Type     `json:"type"`
	At       string   `json:"at,omitempty"`
	Time     string   `json:"time,omitempty"`
	Weekdays []string `json:"weekdays,omitempty"`
	Day      int      `json:"day,omitempty"`
}

type Output struct {
	Format string `json:"format"`
}

type Reservation struct {
	ID              string  `json:"id"`
	Enabled         bool    `json:"enabled"`
	Station         Station `json:"station"`
	Schedule        Rule    `json:"schedule"`
	DurationSeconds int     `json:"duration_seconds"`
	Output          Output  `json:"output"`
}

type Occurrence struct {
	Reservation Reservation
	Start       time.Time
	End         time.Time
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func Parse(data []byte, loc *time.Location) ([]Reservation, error) {
	var reservations []Reservation
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&reservations); err != nil {
		return nil, fmt.Errorf("decode schedules: %w", err)
	}
	var trailer any
	if err := decoder.Decode(&trailer); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("decode schedules: multiple JSON values")
		}
		return nil, fmt.Errorf("decode schedules trailer: %w", err)
	}
	seen := make(map[string]struct{}, len(reservations))
	for i := range reservations {
		if err := reservations[i].Validate(loc); err != nil {
			return nil, fmt.Errorf("schedule %d: %w", i, err)
		}
		if _, exists := seen[reservations[i].ID]; exists {
			return nil, fmt.Errorf("duplicate schedule id %q", reservations[i].ID)
		}
		seen[reservations[i].ID] = struct{}{}
	}
	if a, b, ok := FindOverlap(reservations, loc); ok {
		return nil, fmt.Errorf("overlapping reservations %q and %q", a, b)
	}
	return reservations, nil
}

func (r Reservation) Validate(loc *time.Location) error {
	if !idPattern.MatchString(r.ID) {
		return errors.New("id must be 1..64 safe filename characters")
	}
	if err := device.ValidateStation(r.Station.Mode, r.Station.FrequencyHz); err != nil {
		return fmt.Errorf("station: %w", err)
	}
	if r.DurationSeconds <= 0 || r.DurationSeconds > 7*24*60*60 {
		return errors.New("duration_seconds must be between 1 and 604800")
	}
	if r.Output.Format != "flac" && r.Output.Format != "wav" {
		return errors.New("output.format must be flac or wav")
	}
	switch r.Schedule.Type {
	case OneShot:
		if r.Schedule.At == "" {
			return errors.New("one-shot schedule requires at")
		}
		if _, err := time.Parse(time.RFC3339, r.Schedule.At); err != nil {
			return fmt.Errorf("invalid one-shot at: %w", err)
		}
	case Daily:
		if _, _, err := parseClock(r.Schedule.Time); err != nil {
			return err
		}
	case Weekly:
		if _, _, err := parseClock(r.Schedule.Time); err != nil {
			return err
		}
		if len(r.Schedule.Weekdays) == 0 {
			return errors.New("weekly schedule requires weekdays")
		}
		seen := map[time.Weekday]bool{}
		for _, name := range r.Schedule.Weekdays {
			weekday, ok := parseWeekday(name)
			if !ok {
				return fmt.Errorf("invalid weekday %q", name)
			}
			if seen[weekday] {
				return fmt.Errorf("duplicate weekday %q", name)
			}
			seen[weekday] = true
		}
	case Monthly:
		if _, _, err := parseClock(r.Schedule.Time); err != nil {
			return err
		}
		if r.Schedule.Day < 1 || r.Schedule.Day > 31 {
			return errors.New("monthly schedule day must be between 1 and 31")
		}
	default:
		return fmt.Errorf("unsupported schedule type %q", r.Schedule.Type)
	}
	if loc == nil {
		return errors.New("timezone is required")
	}
	return nil
}

func (r Reservation) Next(after time.Time, loc *time.Location) (time.Time, bool) {
	if !r.Enabled {
		return time.Time{}, false
	}
	switch r.Schedule.Type {
	case OneShot:
		at, err := time.Parse(time.RFC3339, r.Schedule.At)
		if err != nil || !at.After(after) {
			return time.Time{}, false
		}
		return at, true
	case Daily:
		hour, minute, _ := parseClock(r.Schedule.Time)
		return nextMatchingDay(after, loc, hour, minute, func(time.Time) bool { return true }, 2)
	case Weekly:
		hour, minute, _ := parseClock(r.Schedule.Time)
		wanted := map[time.Weekday]bool{}
		for _, name := range r.Schedule.Weekdays {
			weekday, _ := parseWeekday(name)
			wanted[weekday] = true
		}
		return nextMatchingDay(after, loc, hour, minute, func(day time.Time) bool {
			return wanted[day.Weekday()]
		}, 8)
	case Monthly:
		hour, minute, _ := parseClock(r.Schedule.Time)
		localAfter := after.In(loc)
		for offset := 0; offset < 15; offset++ {
			monthStart := time.Date(localAfter.Year(), localAfter.Month()+time.Month(offset), 1, 0, 0, 0, 0, loc)
			candidate := time.Date(monthStart.Year(), monthStart.Month(), r.Schedule.Day, hour, minute, 0, 0, loc)
			if candidate.Month() != monthStart.Month() || candidate.Day() != r.Schedule.Day {
				continue
			}
			if candidate.Hour() != hour || candidate.Minute() != minute {
				continue
			}
			if candidate.After(after) {
				return candidate, true
			}
		}
	}
	return time.Time{}, false
}

func nextMatchingDay(after time.Time, loc *time.Location, hour, minute int, match func(time.Time) bool, limit int) (time.Time, bool) {
	localAfter := after.In(loc)
	start := time.Date(localAfter.Year(), localAfter.Month(), localAfter.Day(), 0, 0, 0, 0, loc)
	for offset := 0; offset < limit; offset++ {
		day := start.AddDate(0, 0, offset)
		if !match(day) {
			continue
		}
		candidate := time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, loc)
		if candidate.Hour() == hour && candidate.Minute() == minute && candidate.After(after) {
			return candidate, true
		}
	}
	return time.Time{}, false
}

func DueBetween(reservations []Reservation, after, through time.Time, loc *time.Location) []Occurrence {
	var due []Occurrence
	for _, reservation := range reservations {
		cursor := after
		for {
			start, ok := reservation.Next(cursor, loc)
			if !ok || start.After(through) {
				break
			}
			due = append(due, Occurrence{
				Reservation: reservation,
				Start:       start,
				End:         start.Add(time.Duration(reservation.DurationSeconds) * time.Second),
			})
			cursor = start
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].Start.Before(due[j].Start) })
	return due
}

func FindOverlap(reservations []Reservation, loc *time.Location) (string, string, bool) {
	for i := range reservations {
		if !reservations[i].Enabled {
			continue
		}
		for j := i + 1; j < len(reservations); j++ {
			if !reservations[j].Enabled {
				continue
			}
			if reservationsOverlap(reservations[i], reservations[j], loc) {
				return reservations[i].ID, reservations[j].ID, true
			}
		}
	}
	return "", "", false
}

func reservationsOverlap(a, b Reservation, loc *time.Location) bool {
	if a.Schedule.Type == OneShot && b.Schedule.Type == OneShot {
		at, _ := time.Parse(time.RFC3339, a.Schedule.At)
		bt, _ := time.Parse(time.RFC3339, b.Schedule.At)
		return intervalsOverlap(at, at.Add(duration(a)), bt, bt.Add(duration(b)))
	}

	start := time.Date(2024, 1, 1, 0, 0, 0, 0, loc)
	end := start.AddDate(2, 0, 0)
	if a.Schedule.Type == OneShot {
		at, _ := time.Parse(time.RFC3339, a.Schedule.At)
		start, end = at.Add(-duration(b)), at.Add(duration(a))
	}
	if b.Schedule.Type == OneShot {
		bt, _ := time.Parse(time.RFC3339, b.Schedule.At)
		start, end = bt.Add(-duration(a)), bt.Add(duration(b))
	}
	aOccurrences := DueBetween([]Reservation{a}, start.Add(-time.Nanosecond), end, loc)
	bOccurrences := DueBetween([]Reservation{b}, start.Add(-time.Nanosecond), end, loc)
	for _, ao := range aOccurrences {
		for _, bo := range bOccurrences {
			if intervalsOverlap(ao.Start, ao.End, bo.Start, bo.End) {
				return true
			}
		}
	}
	return false
}

func intervalsOverlap(aStart, aEnd, bStart, bEnd time.Time) bool {
	return aStart.Before(bEnd) && bStart.Before(aEnd)
}

func duration(r Reservation) time.Duration {
	return time.Duration(r.DurationSeconds) * time.Second
}

func parseClock(value string) (int, int, error) {
	if len(value) != 5 || value[2] != ':' {
		return 0, 0, errors.New("schedule time must use HH:MM")
	}
	hour, errHour := strconv.Atoi(value[:2])
	minute, errMinute := strconv.Atoi(value[3:])
	if errHour != nil || errMinute != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, errors.New("schedule time must use valid 24-hour HH:MM")
	}
	return hour, minute, nil
}

func parseWeekday(name string) (time.Weekday, bool) {
	switch strings.ToLower(name) {
	case "sun":
		return time.Sunday, true
	case "mon":
		return time.Monday, true
	case "tue":
		return time.Tuesday, true
	case "wed":
		return time.Wednesday, true
	case "thu":
		return time.Thursday, true
	case "fri":
		return time.Friday, true
	case "sat":
		return time.Saturday, true
	default:
		return 0, false
	}
}
