package app

import (
	"strconv"
	"strings"
	"time"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// The dashboard trend is a time series, and the parts of it that can be wrong
// are the parts that are pure arithmetic: which bucket a range starts on, how
// the labels are spelled, and whether a quiet hour produces a gap or a zero.
// They live here, away from the database, so they can be tested without one.

const (
	// trendUnitHour and trendUnitDay are the only bucket widths the console
	// asks for. A finer unit would turn a month into tens of thousands of
	// points, which no chart on this page can render.
	trendUnitHour = "hour"
	trendUnitDay  = "day"

	// maxTrendBuckets caps the number of points one request may ask for. It is
	// 720 — thirty days hourly, or about two years daily — which is far more
	// than the page draws and low enough that a hand-crafted URL cannot make
	// the database aggregate a decade into one response.
	maxTrendBuckets = 720

	// trendOffsetLimitMinutes is UTC+14:00, the largest offset in use. The
	// value only ever reaches date_trunc as an interval, so the clamp is about
	// rejecting nonsense rather than about safety.
	trendOffsetLimitMinutes = 14 * 60

	// trendHourLabel and trendDayLabel are the Go spellings of the to_char
	// formats RequestLogSeries is given. They must stay in step with each
	// other: the database labels the buckets and Go regenerates the same
	// labels to fill the gaps, so a mismatch would silently turn every filled
	// bucket into a duplicate.
	trendHourLabel = "2006-01-02 15:04"
	trendDayLabel  = "2006-01-02"
)

// trendUnit normalises and validates the bucket width.
func trendUnit(raw string) (string, bool) {
	switch strings.TrimSpace(raw) {
	case "", trendUnitHour:
		return trendUnitHour, true
	case trendUnitDay:
		return trendUnitDay, true
	default:
		return "", false
	}
}

// trendLabelFormat is the to_char format for a unit, in PostgreSQL's spelling.
func trendLabelFormat(unit string) string {
	if unit == trendUnitDay {
		return "YYYY-MM-DD"
	}
	return "YYYY-MM-DD HH24:00"
}

// trendGoLabelFormat is the same format in Go's reference-time spelling.
func trendGoLabelFormat(unit string) string {
	if unit == trendUnitDay {
		return trendDayLabel
	}
	return trendHourLabel
}

// trendStep is the width of one bucket.
func trendStep(unit string) time.Duration {
	if unit == trendUnitDay {
		return 24 * time.Hour
	}
	return time.Hour
}

// trendOffset turns the caller's UTC offset in minutes into a location.
//
// A fixed offset, not an IANA zone: the browser sends a number, and the whole
// point of the number is that it is what the reader's clock says today. Using
// a named zone would reintroduce the daylight-saving transitions that make
// "24 hours later" land on 23:00 or 01:00 and step the series off its
// boundaries.
func trendOffset(minutes int) *time.Location {
	return time.FixedZone("client", minutes*60)
}

// parseTrendOffset reads the caller's offset. Anything unparseable or outside
// the real range of civil time is treated as UTC rather than rejected: an
// offset is a presentation hint, and failing the whole request over it would
// hide the data the operator actually asked for.
func parseTrendOffset(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0
	}
	if n < -trendOffsetLimitMinutes {
		return -trendOffsetLimitMinutes
	}
	if n > trendOffsetLimitMinutes {
		return trendOffsetLimitMinutes
	}
	return n
}

// alignTrendBucket floors an instant to the start of its bucket in the caller's
// offset. This is the same floor date_trunc performs, which is what lets Go
// generate exactly the labels the database will.
func alignTrendBucket(t time.Time, unit string, loc *time.Location) time.Time {
	l := t.In(loc)
	if unit == trendUnitDay {
		return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc)
	}
	return time.Date(l.Year(), l.Month(), l.Day(), l.Hour(), 0, 0, 0, loc)
}

// fillTrendBuckets turns the rows the database returned into a continuous
// series.
//
// Without this, an hour with no traffic is simply absent, and the chart draws
// the hour before it next to the hour after it as if they were adjacent — a
// quiet night reads as a busy one. Every bucket between the range start and end
// is emitted, with zeroes where nothing was recorded.
func fillTrendBuckets(
	rows []model.RequestLogBucket, from, to time.Time, unit string, offsetMinutes int,
) []model.RequestLogBucket {
	loc := trendOffset(offsetMinutes)
	format := trendGoLabelFormat(unit)
	step := trendStep(unit)

	byLabel := make(map[string]model.RequestLogBucket, len(rows))
	for _, r := range rows {
		byLabel[r.Bucket] = r
	}

	// The capacity is a hint, not a promise: a range wider than the cap was
	// rejected before this ran, so the loop is bounded.
	span := to.Sub(from)/step + 1
	if span < 0 {
		span = 0
	}
	out := make([]model.RequestLogBucket, 0, span)

	for t := alignTrendBucket(from, unit, loc); t.Before(to); t = t.Add(step) {
		label := t.Format(format)
		v, ok := byLabel[label]
		if !ok {
			v = model.RequestLogBucket{Bucket: label}
		}
		v.Bucket = label
		out = append(out, v)
	}
	return out
}
