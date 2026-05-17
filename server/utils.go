package main

import (
	"fmt"
	"math/rand"
	"regexp"
	"strings"
	"time"

	"github.com/pkg/errors"
)

// Random sources, indirected through package vars so tests can stub them
// for deterministic shuffles. math/rand's top-level funcs are auto-seeded
// (Go ≥1.20) and safe for concurrent use.
var (
	randIntn   = func(n int) int { return rand.Intn(n) }
	randInt63n = func(n int64) int64 { return rand.Int63n(n) }
)

// maxWindowMs caps the random window to one day. Longer windows would push
// past the next occurrence on a daily schedule, which we don't want.
const maxWindowMs int64 = 24 * 60 * 60 * 1000

// minLeadTime is the smallest allowed gap between now and the scheduled send time.
// Anything sooner is rejected (server clocks drift; messages need a buffer).
const minLeadTime = 30 * time.Second

// quotedArgsPattern captures quoted strings or bare tokens.
var quotedArgsPattern = regexp.MustCompile(`"([^"]*)"|(\S+)`)

var urlPattern = regexp.MustCompile(`https?://\S+`)

// backtickURLs wraps any http(s) URLs in s with backticks so they render as
// inline code in markdown — keeps Mattermost from auto-linking URLs in the
// list ephemeral and triggering link previews on a summary of a future post.
func backtickURLs(s string) string {
	return urlPattern.ReplaceAllStringFunc(s, func(u string) string {
		return "`" + u + "`"
	})
}

// parseCommandArgs splits a slash-command argument string preserving quoted segments.
// Example: `"hello world" "2025-06-01 09:30" UTC` -> ["hello world", "2025-06-01 09:30", "UTC"]
func parseCommandArgs(input string) []string {
	matches := quotedArgsPattern.FindAllStringSubmatch(input, -1)
	args := make([]string, 0, len(matches))
	for _, m := range matches {
		if strings.HasPrefix(m[0], `"`) {
			// quoted segment — keep even if empty
			args = append(args, m[1])
		} else if m[2] != "" {
			args = append(args, m[2])
		}
	}
	return args
}

// parseScheduleTime accepts ISO-8601 (`2025-06-01T09:30:00Z`) or `YYYY-MM-DD HH:MM` plus
// an optional IANA timezone (e.g. "Australia/Sydney"). Empty timezone means UTC.
func parseScheduleTime(timeStr, tz string) (time.Time, error) {
	timeStr = strings.TrimSpace(timeStr)
	if timeStr == "" {
		return time.Time{}, errors.New("send time is required")
	}

	loc := time.UTC
	if tz != "" {
		l, err := time.LoadLocation(tz)
		if err != nil {
			return time.Time{}, errors.Wrapf(err, "invalid timezone %q", tz)
		}
		loc = l
	}

	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02T15:04",
	}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, timeStr, loc); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("could not parse time %q (try YYYY-MM-DD HH:MM)", timeStr)
}

// validateSendAt ensures the send time is sufficiently in the future.
func validateSendAt(sendAt time.Time, now time.Time) error {
	if sendAt.Before(now.Add(minLeadTime)) {
		return fmt.Errorf("send time must be at least %s in the future", minLeadTime)
	}
	return nil
}

// formatSendAt renders a Unix-ms timestamp in the given IANA timezone (or UTC).
func formatSendAt(sendAt int64, tz string) string {
	loc := time.UTC
	if tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	return time.UnixMilli(sendAt).In(loc).Format("Mon 2 Jan 2006 at 3:04 PM MST")
}

// truncate returns the first n runes of s, appending "…" if truncated.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// nextOccurrence returns the next send time for a recurring message. It works
// in the user's IANA timezone using wall-clock arithmetic, so DST transitions
// don't shift "9 AM Sydney" into "8 AM" or "10 AM" the following week.
func nextOccurrence(current time.Time, repeat, tz string) (time.Time, error) {
	loc := time.UTC
	if tz != "" {
		l, err := time.LoadLocation(tz)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid timezone %q: %w", tz, err)
		}
		loc = l
	}
	local := current.In(loc)

	switch repeat {
	case RepeatDaily:
		return local.AddDate(0, 0, 1).UTC(), nil

	case RepeatWeekdays:
		next := local.AddDate(0, 0, 1)
		switch next.Weekday() {
		case time.Saturday:
			next = next.AddDate(0, 0, 2)
		case time.Sunday:
			next = next.AddDate(0, 0, 1)
		}
		return next.UTC(), nil

	case RepeatWeekly:
		return local.AddDate(0, 0, 7).UTC(), nil

	case RepeatFortnightly:
		return local.AddDate(0, 0, 14).UTC(), nil

	case RepeatMonthly:
		// Build the target wall-clock time directly. time.Date normalises
		// invalid dates by overflowing (e.g. Feb 30 → Mar 2), so detect that
		// and clamp to the last day of the *target* month.
		y, m, d := local.Date()
		hour, min, sec := local.Clock()
		target := time.Date(y, m+1, d, hour, min, sec, local.Nanosecond(), loc)
		if target.Day() != d {
			// Overflowed — last day of target month is day-0 of month+2.
			target = time.Date(y, m+2, 0, hour, min, sec, local.Nanosecond(), loc)
		}
		return target.UTC(), nil

	case RepeatYearly:
		// Same wall-clock day next year. The only overflow case is Feb 29
		// in a leap year → next year is non-leap; clamp to Feb 28.
		y, m, d := local.Date()
		hour, min, sec := local.Clock()
		target := time.Date(y+1, m, d, hour, min, sec, local.Nanosecond(), loc)
		if target.Month() != m {
			// Overflowed (Feb 29 → Mar 1). Clamp to last day of original month.
			target = time.Date(y+1, m+1, 0, hour, min, sec, local.Nanosecond(), loc)
		}
		return target.UTC(), nil
	}

	return time.Time{}, fmt.Errorf("unsupported repeat %q", repeat)
}

// seriesEnded reports whether a recurring message's series should stop *before*
// the next occurrence. `next` is the time nextOccurrence returned.
func seriesEnded(msg *ScheduledMessage, next time.Time) bool {
	switch msg.EndsMode {
	case EndsAfter:
		return msg.Occurrences >= msg.EndsAfter
	case EndsOn:
		return next.UnixMilli() > msg.EndsAt
	}
	return false
}

// validateExtras checks the time-window and multi-message rotation fields.
// Called alongside validateRecurrence by the API handlers.
func validateExtras(repeat string, windowMs int64, messages []string) error {
	if windowMs < 0 {
		return errors.New("window_ms must be >= 0")
	}
	if windowMs > maxWindowMs {
		return fmt.Errorf("window_ms must be <= %d (24h)", maxWindowMs)
	}
	if len(messages) > 0 {
		if repeat == RepeatNone {
			return errors.New("multiple messages require a repeating schedule")
		}
		if len(messages) < 2 {
			return errors.New("provide at least 2 messages, or use the single-message field")
		}
		for i, m := range messages {
			if strings.TrimSpace(m) == "" {
				return fmt.Errorf("message %d is empty", i+1)
			}
		}
	}
	return nil
}

// newShuffledCycle returns a random permutation of [0..n). If avoid is
// non-nil and in range and the freshly-shuffled cycle would start with that
// index, cycle[0] and cycle[1] are swapped (when n >= 2) so a new cycle
// doesn't immediately repeat the most-recently-sent message.
func newShuffledCycle(n int, avoid *int) []int {
	if n <= 0 {
		return nil
	}
	cycle := make([]int, n)
	for i := range cycle {
		cycle[i] = i
	}
	// Fisher–Yates.
	for i := n - 1; i > 0; i-- {
		j := randIntn(i + 1)
		cycle[i], cycle[j] = cycle[j], cycle[i]
	}
	if avoid != nil && n >= 2 && *avoid >= 0 && *avoid < n && cycle[0] == *avoid {
		cycle[0], cycle[1] = cycle[1], cycle[0]
	}
	return cycle
}

// pickMessageBody decides which message to send for a scheduled message and
// returns the body plus the rotation index used (-1 when there's no rotation).
// For rotating schedules it ensures msg.MessageCycle is valid, regenerating
// it (and resetting MessageCyclePos) whenever the cycle is exhausted, empty,
// or contains indices that are no longer valid (e.g. after an edit shrank
// the Messages list). Mutates msg in place; callers must persist the result.
func pickMessageBody(msg *ScheduledMessage) (string, int) {
	if len(msg.Messages) == 0 {
		return msg.Message, -1
	}
	if needsNewCycle(msg) {
		msg.MessageCycle = newShuffledCycle(len(msg.Messages), msg.LastSentIndex)
		msg.MessageCyclePos = 0
	}
	idx := msg.MessageCycle[msg.MessageCyclePos]
	return msg.Messages[idx], idx
}

func needsNewCycle(msg *ScheduledMessage) bool {
	if len(msg.MessageCycle) == 0 {
		return true
	}
	if msg.MessageCyclePos < 0 || msg.MessageCyclePos >= len(msg.MessageCycle) {
		return true
	}
	for _, i := range msg.MessageCycle {
		if i < 0 || i >= len(msg.Messages) {
			return true
		}
	}
	return false
}

// trimMessages strips whitespace from each entry and drops any empties. A
// nil/empty input returns nil so downstream "len == 0" checks Just Work.
func trimMessages(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, m := range in {
		t := strings.TrimSpace(m)
		if t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// equalStrings reports whether two string slices contain the same values in
// the same order. Used by the API to detect rotation-list changes on update.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// validateRecurrence checks that the recurrence fields on a message are
// internally consistent. Called from the API handler before persisting.
func validateRecurrence(repeat, endsMode string, endsAfter int, hasEndsAt bool) error {
	switch repeat {
	case RepeatNone, RepeatDaily, RepeatWeekdays, RepeatWeekly, RepeatFortnightly, RepeatMonthly, RepeatYearly:
		// ok
	default:
		return fmt.Errorf("invalid repeat %q", repeat)
	}
	if repeat == RepeatNone {
		if endsMode != "" || endsAfter != 0 || hasEndsAt {
			return fmt.Errorf("ends_* fields require a non-empty repeat")
		}
		return nil
	}
	switch endsMode {
	case "", EndsNever:
		// ok
	case EndsOn:
		if !hasEndsAt {
			return fmt.Errorf("ends_on date is required when ends_mode=on")
		}
	case EndsAfter:
		if endsAfter < 1 {
			return fmt.Errorf("ends_after must be >= 1 when ends_mode=after")
		}
	default:
		return fmt.Errorf("invalid ends_mode %q", endsMode)
	}
	return nil
}
