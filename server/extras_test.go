package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mattermost/mattermost-server/v6/model"
	"github.com/mattermost/mattermost-server/v6/plugin/plugintest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// withDeterministicRand swaps the package-level random hooks for the duration
// of a test. intnFn / int63nFn can encode any wanted sequence.
func withDeterministicRand(t *testing.T, intnFn func(int) int, int63nFn func(int64) int64) {
	t.Helper()
	prevIntn := randIntn
	prevInt63n := randInt63n
	if intnFn != nil {
		randIntn = intnFn
	}
	if int63nFn != nil {
		randInt63n = int63nFn
	}
	t.Cleanup(func() {
		randIntn = prevIntn
		randInt63n = prevInt63n
	})
}

func TestEffectiveFireAt(t *testing.T) {
	m := &ScheduledMessage{SendAt: 1000}
	assert.Equal(t, int64(1000), m.effectiveFireAt())
	m.FireAt = 1500
	assert.Equal(t, int64(1500), m.effectiveFireAt())
}

func TestNewShuffledCycleIsPermutation(t *testing.T) {
	// Fixed shuffle so the result is reproducible.
	withDeterministicRand(t, func(n int) int { return 0 }, nil)
	c := newShuffledCycle(5, nil)
	require.Len(t, c, 5)
	seen := map[int]bool{}
	for _, v := range c {
		seen[v] = true
	}
	assert.Len(t, seen, 5, "every index must appear exactly once")
}

func TestNewShuffledCycleHandlesEdgeSizes(t *testing.T) {
	assert.Nil(t, newShuffledCycle(0, nil))
	assert.Equal(t, []int{0}, newShuffledCycle(1, nil))
}

func TestNewShuffledCycleAvoidsBoundaryRepeat(t *testing.T) {
	// Pin shuffle to identity so cycle starts as [0,1,2,3].
	withDeterministicRand(t, func(n int) int { return n - 1 }, nil)
	avoid := 0
	c := newShuffledCycle(4, &avoid)
	require.NotEmpty(t, c)
	assert.NotEqual(t, avoid, c[0], "swap should have moved the avoided index off the front")
}

func TestNewShuffledCycleAvoidNoOpWhenSizeOne(t *testing.T) {
	withDeterministicRand(t, func(n int) int { return 0 }, nil)
	avoid := 0
	// With n=1 the swap is impossible and we must just return [0].
	c := newShuffledCycle(1, &avoid)
	assert.Equal(t, []int{0}, c)
}

func TestPickMessageBodySingleMessage(t *testing.T) {
	msg := &ScheduledMessage{Message: "only"}
	body, idx := pickMessageBody(msg)
	assert.Equal(t, "only", body)
	assert.Equal(t, -1, idx)
}

func TestPickMessageBodyBuildsCycleWhenMissing(t *testing.T) {
	withDeterministicRand(t, func(n int) int { return 0 }, nil)
	msg := &ScheduledMessage{Messages: []string{"a", "b", "c"}}
	body, idx := pickMessageBody(msg)
	assert.NotEmpty(t, body)
	assert.GreaterOrEqual(t, idx, 0)
	assert.Len(t, msg.MessageCycle, 3)
	assert.Equal(t, 0, msg.MessageCyclePos)
}

func TestPickMessageBodyRebuildsAfterEditShrunkList(t *testing.T) {
	withDeterministicRand(t, func(n int) int { return 0 }, nil)
	msg := &ScheduledMessage{
		Messages:        []string{"a", "b"},
		MessageCycle:    []int{0, 1, 2}, // stale: contains 2 which is now out of range
		MessageCyclePos: 0,
	}
	_, _ = pickMessageBody(msg)
	for _, i := range msg.MessageCycle {
		assert.Less(t, i, len(msg.Messages))
		assert.GreaterOrEqual(t, i, 0)
	}
}

func TestValidateExtrasRejectsBadInputs(t *testing.T) {
	cases := []struct {
		name    string
		repeat  string
		window  int64
		msgs    []string
		wantErr string
	}{
		{"negative window", "daily", -1, nil, "window_ms"},
		{"window too large", "daily", maxWindowMs + 1, nil, "24h"},
		{"messages without repeat", "", 0, []string{"a", "b"}, "repeating schedule"},
		{"single-entry messages", "daily", 0, []string{"a"}, "at least 2"},
		{"empty message in list", "daily", 0, []string{"a", "   "}, "is empty"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateExtras(c.repeat, c.window, c.msgs)
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.wantErr)
		})
	}
}

func TestValidateExtrasAcceptsValid(t *testing.T) {
	require.NoError(t, validateExtras("", 0, nil))
	require.NoError(t, validateExtras("daily", 30*60*1000, nil))
	require.NoError(t, validateExtras("daily", 0, []string{"a", "b"}))
	require.NoError(t, validateExtras("weekly", maxWindowMs, []string{"a", "b", "c"}))
}

func TestEqualStrings(t *testing.T) {
	assert.True(t, equalStrings(nil, nil))
	assert.True(t, equalStrings([]string{}, []string{}))
	assert.True(t, equalStrings([]string{"a", "b"}, []string{"a", "b"}))
	assert.False(t, equalStrings([]string{"a"}, []string{"a", "b"}))
	assert.False(t, equalStrings([]string{"a", "b"}, []string{"b", "a"}))
}

func TestTrimMessages(t *testing.T) {
	assert.Nil(t, trimMessages(nil))
	assert.Nil(t, trimMessages([]string{"", "   "}))
	assert.Equal(t, []string{"a", "b"}, trimMessages([]string{"  a  ", "", "b"}))
}

func TestRandomFireAt(t *testing.T) {
	withDeterministicRand(t, nil, func(n int64) int64 { return n / 2 })
	got := randomFireAt(1000, 200)
	assert.Equal(t, int64(1100), got)

	// Zero window means "no window" — signal with FireAt = 0.
	assert.Equal(t, int64(0), randomFireAt(1000, 0))
	assert.Equal(t, int64(0), randomFireAt(1000, -1))
}

func TestRoundTripWithAllNewFields(t *testing.T) {
	idx := 2
	orig := &ScheduledMessage{
		ID: "m1", UserID: "u1", ChannelID: "c1", Message: "primary",
		SendAt: 1_000_000, CreatedAt: 999_999, Status: StatusPending,
		Repeat: RepeatDaily, Timezone: "Australia/Sydney",
		WindowMs: 30 * 60 * 1000, FireAt: 1_500_000,
		Messages: []string{"a", "b", "c"}, MessageCycle: []int{2, 0, 1},
		MessageCyclePos: 1, LastSentIndex: &idx,
	}
	data, err := json.Marshal(orig)
	require.NoError(t, err)

	var got ScheduledMessage
	require.NoError(t, json.Unmarshal(data, &got))
	require.NotNil(t, got.LastSentIndex)
	assert.Equal(t, idx, *got.LastSentIndex)
	assert.Equal(t, orig.MessageCycle, got.MessageCycle)
	assert.Equal(t, orig.MessageCyclePos, got.MessageCyclePos)
	assert.Equal(t, orig.WindowMs, got.WindowMs)
	assert.Equal(t, orig.FireAt, got.FireAt)
	assert.Equal(t, orig.Messages, got.Messages)
}

func TestRoundTripNilLastSentIndexOmitted(t *testing.T) {
	orig := &ScheduledMessage{ID: "m1", UserID: "u1", Message: "x", SendAt: 1}
	data, err := json.Marshal(orig)
	require.NoError(t, err)
	// omitempty pointer should drop the key entirely so it's nil after round-trip.
	assert.NotContains(t, string(data), "last_sent_index")

	var got ScheduledMessage
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Nil(t, got.LastSentIndex)
}

// --- Scheduler-level behaviour ----------------------------------------------

func TestSendDueMessagesRespectsFireAt(t *testing.T) {
	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	p := newPluginWithConfig(api, &configuration{PollIntervalSeconds: 30, MaxAttempts: 3})

	now := time.Now().UTC()
	// SendAt is already in the past, but FireAt is well in the future:
	// the scheduler must wait.
	future := &ScheduledMessage{
		ID: "m1", UserID: "u1", ChannelID: "c1", Message: "later",
		SendAt:   now.Add(-time.Hour).UnixMilli(),
		WindowMs: int64(2 * time.Hour / time.Millisecond),
		FireAt:   now.Add(time.Hour).UnixMilli(),
		Status:   StatusPending,
	}
	enc, _ := json.Marshal(future)

	api.On("KVList", 0, listPageSize).Return([]string{"scheduled_u1_m1"}, nil)
	api.On("KVGet", "scheduled_u1_m1").Return(enc, nil).Once()
	// CreatePost / lock should NOT be called.

	p.sendDueMessages()
}

func TestDispatchPicksRotatingMessage(t *testing.T) {
	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	p := newPluginWithConfig(api, &configuration{PollIntervalSeconds: 30, MaxAttempts: 3})

	// Pin shuffle to identity → cycle becomes [0, 1, 2]; pos 0 is "alpha".
	withDeterministicRand(t, func(n int) int { return n - 1 }, func(n int64) int64 { return 0 })

	msg := &ScheduledMessage{
		ID: "m1", UserID: "u1", ChannelID: "c1", Message: "alpha",
		Messages: []string{"alpha", "beta", "gamma"},
		SendAt:   time.Now().Add(-time.Second).UnixMilli(),
		Status:   StatusPending, Repeat: RepeatDaily, Timezone: "UTC",
	}
	encoded, _ := json.Marshal(msg)

	api.On("KVSetWithOptions", "lock_m1", []byte("1"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil)
	api.On("KVGet", "scheduled_u1_m1").Return(encoded, nil)
	api.On("CreatePost", mock.MatchedBy(func(p *model.Post) bool {
		return p.Message == "alpha"
	})).Return(&model.Post{Id: "p1"}, (*model.AppError)(nil))
	// advanceRecurring saves the updated message; assert cycle pos advanced
	// and LastSentIndex captures the freshly-sent index.
	api.On("KVSet", "scheduled_u1_m1", mock.MatchedBy(func(b []byte) bool {
		var saved ScheduledMessage
		if err := json.Unmarshal(b, &saved); err != nil {
			return false
		}
		if saved.MessageCyclePos != 1 {
			return false
		}
		if saved.LastSentIndex == nil || *saved.LastSentIndex != 0 {
			return false
		}
		return saved.Occurrences == 1
	})).Return(nil)
	api.On("KVDelete", "lock_m1").Return(nil)

	p.dispatch(msg, 3)
}

func TestRotationCyclesAllBeforeRepeating(t *testing.T) {
	// End-to-end: dispatch a rotating recurring schedule 6 times and confirm
	// each set of 3 covers indices {0,1,2} exactly once.
	withDeterministicRand(t, func(n int) int { return n - 1 }, func(n int64) int64 { return 0 })

	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	p := newPluginWithConfig(api, &configuration{PollIntervalSeconds: 30, MaxAttempts: 3})

	msg := &ScheduledMessage{
		ID: "m1", UserID: "u1", ChannelID: "c1", Message: "alpha",
		Messages: []string{"alpha", "beta", "gamma"},
		SendAt:   time.Now().Add(-time.Second).UnixMilli(),
		Status:   StatusPending, Repeat: RepeatDaily, Timezone: "UTC",
	}

	var sent []string
	api.On("KVSetWithOptions", "lock_m1", []byte("1"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil)
	// Loaded state is the in-memory `current` between dispatches.
	current := *msg
	api.On("KVGet", "scheduled_u1_m1").Return(func(_ string) []byte {
		b, _ := json.Marshal(&current)
		return b
	}, func(_ string) *model.AppError { return nil })
	api.On("CreatePost", mock.MatchedBy(func(p *model.Post) bool {
		sent = append(sent, p.Message)
		return true
	})).Return(&model.Post{Id: "p1"}, (*model.AppError)(nil))
	api.On("KVSet", "scheduled_u1_m1", mock.Anything).Return(func(_ string, b []byte) *model.AppError {
		var saved ScheduledMessage
		if err := json.Unmarshal(b, &saved); err == nil {
			current = saved
		}
		return nil
	})
	api.On("KVDelete", "lock_m1").Return(nil)

	for i := 0; i < 6; i++ {
		// Pretend the message is due now for each iteration.
		current.SendAt = time.Now().Add(-time.Second).UnixMilli()
		current.FireAt = 0
		current.Status = StatusPending
		next := current
		p.dispatch(&next, 3)
	}

	require.Len(t, sent, 6)
	cycle1 := map[string]int{sent[0]: 0, sent[1]: 0, sent[2]: 0}
	cycle2 := map[string]int{sent[3]: 0, sent[4]: 0, sent[5]: 0}
	assert.Len(t, cycle1, 3, "first cycle must hit all three messages")
	assert.Len(t, cycle2, 3, "second cycle must hit all three messages")
	for i := 1; i < len(sent); i++ {
		assert.NotEqual(t, sent[i-1], sent[i], "no back-to-back repeats across boundaries")
	}
}

func TestAdvanceRecurringRerandomisesFireAtWithinWindow(t *testing.T) {
	// Force randInt63n to return 0 then check FireAt == next SendAt anchor.
	withDeterministicRand(t, func(n int) int { return 0 }, func(n int64) int64 { return n / 4 })

	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	p := newPluginWithConfig(api, &configuration{PollIntervalSeconds: 30, MaxAttempts: 3})

	utc := "UTC"
	start := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC).UnixMilli()
	msg := &ScheduledMessage{
		ID: "m1", UserID: "u1", ChannelID: "c1", Message: "hello",
		SendAt:   start,
		Status:   StatusPending,
		Repeat:   RepeatDaily,
		Timezone: utc,
		WindowMs: 60 * 60 * 1000, // 1h
		FireAt:   start + 30*60*1000,
	}

	var saved ScheduledMessage
	api.On("KVSet", "scheduled_u1_m1", mock.MatchedBy(func(b []byte) bool {
		return json.Unmarshal(b, &saved) == nil
	})).Return(nil)

	p.advanceRecurring(msg, -1)

	expectedNext := time.Date(2026, 5, 2, 9, 0, 0, 0, time.UTC).UnixMilli()
	assert.Equal(t, expectedNext, saved.SendAt, "anchor advances by exactly one day")
	assert.Equal(t, expectedNext+(60*60*1000)/4, saved.FireAt, "FireAt is anchor + random offset")
	assert.GreaterOrEqual(t, saved.FireAt-saved.SendAt, int64(0))
	assert.Less(t, saved.FireAt-saved.SendAt, msg.WindowMs)
}

func TestAdvanceRecurringClearsFireAtWhenSeriesEnds(t *testing.T) {
	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	p := newPluginWithConfig(api, &configuration{PollIntervalSeconds: 30, MaxAttempts: 3})

	start := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC).UnixMilli()
	msg := &ScheduledMessage{
		ID: "m1", UserID: "u1", Message: "hi",
		SendAt: start, Status: StatusPending,
		Repeat: RepeatDaily, Timezone: "UTC",
		EndsMode: EndsAfter, EndsAfter: 1, Occurrences: 0,
		WindowMs: 60 * 60 * 1000, FireAt: start + 100,
	}

	var saved ScheduledMessage
	api.On("KVSet", "scheduled_u1_m1", mock.MatchedBy(func(b []byte) bool {
		return json.Unmarshal(b, &saved) == nil
	})).Return(nil)

	p.advanceRecurring(msg, -1)

	assert.Equal(t, StatusCompleted, saved.Status)
	assert.Equal(t, int64(0), saved.FireAt, "completed series should drop FireAt")
}

// --- API-level behaviour ----------------------------------------------------

func TestApiUpdateResetsRotationOnMessagesChange(t *testing.T) {
	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	p := newPluginWithConfig(api, &configuration{PollIntervalSeconds: 30, MaxAttempts: 3})

	idx := 1
	existing := &ScheduledMessage{
		ID: "m1", UserID: "u1", ChannelID: "c1", Message: "alpha",
		SendAt:   time.Now().Add(time.Hour).UnixMilli(),
		Status:   StatusPending,
		Repeat:   RepeatDaily,
		Timezone: "UTC",
		Messages: []string{"alpha", "beta"},
		MessageCycle: []int{1, 0}, MessageCyclePos: 1, LastSentIndex: &idx,
	}
	existingBytes, _ := json.Marshal(existing)

	api.On("KVGet", "scheduled_u1_m1").Return(existingBytes, nil)
	api.On("GetChannelMember", "c1", "u1").Return(&model.ChannelMember{}, (*model.AppError)(nil))

	var saved ScheduledMessage
	api.On("KVSet", "scheduled_u1_m1", mock.MatchedBy(func(b []byte) bool {
		return json.Unmarshal(b, &saved) == nil
	})).Return(nil)

	body := updatePayload{
		ID:        "m1",
		ChannelID: "c1",
		Message:   "alpha",
		Messages:  []string{"alpha", "beta", "gamma"}, // changed → reset
		SendAt:    time.Now().Add(2 * time.Hour).UTC().Format("2006-01-02T15:04:05Z"),
		Timezone:  "UTC",
		Repeat:    RepeatDaily,
		EndsMode:  EndsNever,
	}
	payloadJSON, _ := json.Marshal(body)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("PATCH", "/api/update", bytes.NewReader(payloadJSON))
	p.apiUpdate(w, r, "u1")
	require.Equal(t, 200, w.Code, "update should succeed; got body=%s", w.Body.String())

	assert.Equal(t, []string{"alpha", "beta", "gamma"}, saved.Messages)
	assert.Nil(t, saved.MessageCycle)
	assert.Equal(t, 0, saved.MessageCyclePos)
	assert.Nil(t, saved.LastSentIndex)
}
