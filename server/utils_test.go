package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCommandArgs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"two quoted", `"hello world" "2025-06-01 09:30"`, []string{"hello world", "2025-06-01 09:30"}},
		{"quoted plus tz", `"msg" "2025-06-01 09:30" Australia/Sydney`, []string{"msg", "2025-06-01 09:30", "Australia/Sydney"}},
		{"list", `list`, []string{"list"}},
		{"cancel id", `cancel abc123`, []string{"cancel", "abc123"}},
		{"empty quoted preserved", `"" "2025-06-01 09:30"`, []string{"", "2025-06-01 09:30"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseCommandArgs(c.in)
			if len(c.want) == 0 {
				assert.Empty(t, got)
				return
			}
			assert.Equal(t, c.want, got)
		})
	}
}

func TestParseScheduleTime(t *testing.T) {
	t.Run("RFC3339 UTC", func(t *testing.T) {
		got, err := parseScheduleTime("2025-06-01T09:30:00Z", "")
		require.NoError(t, err)
		assert.Equal(t, time.Date(2025, 6, 1, 9, 30, 0, 0, time.UTC), got)
	})

	t.Run("local with timezone", func(t *testing.T) {
		got, err := parseScheduleTime("2025-06-01 09:30", "Australia/Sydney")
		require.NoError(t, err)
		// Sydney is UTC+10 in June (no DST).
		assert.Equal(t, time.Date(2025, 5, 31, 23, 30, 0, 0, time.UTC), got)
	})

	t.Run("naked datetime defaults UTC", func(t *testing.T) {
		got, err := parseScheduleTime("2025-06-01 09:30", "")
		require.NoError(t, err)
		assert.Equal(t, time.Date(2025, 6, 1, 9, 30, 0, 0, time.UTC), got)
	})

	t.Run("invalid timezone", func(t *testing.T) {
		_, err := parseScheduleTime("2025-06-01 09:30", "Mars/Olympus")
		require.Error(t, err)
	})

	t.Run("invalid format", func(t *testing.T) {
		_, err := parseScheduleTime("not a date", "")
		require.Error(t, err)
	})

	t.Run("empty", func(t *testing.T) {
		_, err := parseScheduleTime("", "")
		require.Error(t, err)
	})
}

func TestValidateSendAt(t *testing.T) {
	now := time.Now().UTC()
	assert.NoError(t, validateSendAt(now.Add(2*time.Minute), now))
	assert.Error(t, validateSendAt(now.Add(5*time.Second), now))
	assert.Error(t, validateSendAt(now.Add(-time.Hour), now))
}

func TestTruncate(t *testing.T) {
	assert.Equal(t, "", truncate("anything", 0))
	assert.Equal(t, "abc", truncate("abc", 5))
	assert.Equal(t, "abcde…", truncate("abcdefghij", 5))
}

func TestKeyHelpers(t *testing.T) {
	key := messageKey("user1", "msgA")
	assert.Equal(t, "scheduled_user1_msgA", key)

	prefix := userPrefix("user1")
	assert.Equal(t, "scheduled_user1_", prefix)

	u, m, ok := parseUserAndMessageID("scheduled_user1_msgA")
	require.True(t, ok)
	assert.Equal(t, "user1", u)
	assert.Equal(t, "msgA", m)

	_, _, ok = parseUserAndMessageID("foo_bar")
	assert.False(t, ok)
}
