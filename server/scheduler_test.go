package main

import (
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattermost/mattermost-server/v6/model"
	"github.com/mattermost/mattermost-server/v6/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newPluginWithConfig(api *plugintest.API, cfg *configuration) *Plugin {
	p := &Plugin{}
	p.SetAPI(api)
	p.configuration = atomic.Value{}
	p.setConfiguration(cfg)
	return p
}

func TestDispatchSendsAndDeletes(t *testing.T) {
	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	p := newPluginWithConfig(api, &configuration{PollIntervalSeconds: 30, MaxAttempts: 3})

	msg := &ScheduledMessage{
		ID:        "m1",
		ChannelID: "c1",
		UserID:    "u1",
		Message:   "hi",
		SendAt:    time.Now().Add(-time.Second).UnixMilli(),
		Status:    StatusPending,
	}
	encoded, _ := json.Marshal(msg)

	api.On("KVSetWithOptions", "lock_m1", []byte("1"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil)
	api.On("KVGet", "scheduled_u1_m1").Return(encoded, nil)
	api.On("CreatePost", mock.MatchedBy(func(p *model.Post) bool {
		return p.ChannelId == "c1" && p.UserId == "u1" && p.Message == "hi"
	})).Return(&model.Post{Id: "post1"}, (*model.AppError)(nil))
	api.On("KVDelete", "scheduled_u1_m1").Return(nil)
	api.On("KVDelete", "lock_m1").Return(nil)

	p.dispatch(msg, 3)
}

func TestDispatchSkipsWhenLockUnavailable(t *testing.T) {
	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	p := newPluginWithConfig(api, &configuration{PollIntervalSeconds: 30, MaxAttempts: 3})

	msg := &ScheduledMessage{ID: "m1", UserID: "u1", Status: StatusPending}
	api.On("KVSetWithOptions", "lock_m1", []byte("1"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(false, nil)

	p.dispatch(msg, 3)
	// no other expectations — proves dispatch returned early
}

func TestDispatchRetriesOnFailure(t *testing.T) {
	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	p := newPluginWithConfig(api, &configuration{PollIntervalSeconds: 30, MaxAttempts: 3})

	msg := &ScheduledMessage{
		ID: "m1", ChannelID: "c1", UserID: "u1", Message: "hi",
		SendAt: time.Now().Add(-time.Second).UnixMilli(),
		Status: StatusPending, Attempts: 1,
	}
	encoded, _ := json.Marshal(msg)

	api.On("KVSetWithOptions", "lock_m1", []byte("1"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil)
	api.On("KVGet", "scheduled_u1_m1").Return(encoded, nil)
	appErr := model.NewAppError("CreatePost", "boom", nil, "channel deleted", 500)
	api.On("CreatePost", mock.AnythingOfType("*model.Post")).Return((*model.Post)(nil), appErr)

	api.On("KVSet", "scheduled_u1_m1", mock.MatchedBy(func(b []byte) bool {
		var saved ScheduledMessage
		if err := json.Unmarshal(b, &saved); err != nil {
			return false
		}
		// attempts incremented to 2, status still pending (under max=3)
		return saved.Attempts == 2 && saved.Status == StatusPending && saved.LastError != ""
	})).Return(nil)

	api.On("LogWarn", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()
	api.On("KVDelete", "lock_m1").Return(nil)

	p.dispatch(msg, 3)
}

func TestDispatchMarksFailedAtMaxAttempts(t *testing.T) {
	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	p := newPluginWithConfig(api, &configuration{PollIntervalSeconds: 30, MaxAttempts: 3})

	msg := &ScheduledMessage{
		ID: "m1", ChannelID: "c1", UserID: "u1", Message: "hi",
		Status: StatusPending, Attempts: 2, // one more failure pushes to 3 = max
	}
	encoded, _ := json.Marshal(msg)

	api.On("KVSetWithOptions", "lock_m1", []byte("1"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil)
	api.On("KVGet", "scheduled_u1_m1").Return(encoded, nil)
	appErr := model.NewAppError("CreatePost", "boom", nil, "channel deleted", 500)
	api.On("CreatePost", mock.AnythingOfType("*model.Post")).Return((*model.Post)(nil), appErr)

	api.On("KVSet", "scheduled_u1_m1", mock.MatchedBy(func(b []byte) bool {
		var saved ScheduledMessage
		if err := json.Unmarshal(b, &saved); err != nil {
			return false
		}
		return saved.Attempts == 3 && saved.Status == StatusFailed
	})).Return(nil)

	api.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()
	api.On("KVDelete", "lock_m1").Return(nil)

	p.dispatch(msg, 3)
}

func TestDispatchSkipsWhenStatusChanged(t *testing.T) {
	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	p := newPluginWithConfig(api, &configuration{PollIntervalSeconds: 30, MaxAttempts: 3})

	stale := &ScheduledMessage{ID: "m1", UserID: "u1", Status: StatusPending}
	current := &ScheduledMessage{ID: "m1", UserID: "u1", Status: StatusFailed}
	currentBytes, _ := json.Marshal(current)

	api.On("KVSetWithOptions", "lock_m1", []byte("1"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil)
	api.On("KVGet", "scheduled_u1_m1").Return(currentBytes, nil)
	api.On("KVDelete", "lock_m1").Return(nil)
	// CreatePost should NOT be called

	p.dispatch(stale, 3)
}

func TestSendDueMessagesOnlyDispatchesDueAndPending(t *testing.T) {
	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	p := newPluginWithConfig(api, &configuration{PollIntervalSeconds: 30, MaxAttempts: 3})

	now := time.Now().UTC()
	due := &ScheduledMessage{ID: "due", UserID: "u1", ChannelID: "c1", Message: "go",
		SendAt: now.Add(-time.Minute).UnixMilli(), Status: StatusPending}
	future := &ScheduledMessage{ID: "future", UserID: "u1", ChannelID: "c1", Message: "later",
		SendAt: now.Add(time.Hour).UnixMilli(), Status: StatusPending}
	failed := &ScheduledMessage{ID: "failed", UserID: "u1", ChannelID: "c1", Message: "no",
		SendAt: now.Add(-time.Hour).UnixMilli(), Status: StatusFailed}

	dueB, _ := json.Marshal(due)
	futureB, _ := json.Marshal(future)
	failedB, _ := json.Marshal(failed)

	api.On("KVList", 0, listPageSize).Return([]string{
		"scheduled_u1_due", "scheduled_u1_future", "scheduled_u1_failed",
	}, nil)
	api.On("KVGet", "scheduled_u1_due").Return(dueB, nil).Once()
	api.On("KVGet", "scheduled_u1_future").Return(futureB, nil).Once()
	api.On("KVGet", "scheduled_u1_failed").Return(failedB, nil).Once()

	// Dispatch path for the "due" message:
	api.On("KVSetWithOptions", "lock_due", []byte("1"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil)
	api.On("KVGet", "scheduled_u1_due").Return(dueB, nil).Once() // re-fetch inside dispatch
	api.On("CreatePost", mock.AnythingOfType("*model.Post")).Return(&model.Post{Id: "p1"}, (*model.AppError)(nil))
	api.On("KVDelete", "scheduled_u1_due").Return(nil)
	api.On("KVDelete", "lock_due").Return(nil)

	p.sendDueMessages()
}

func TestSendDueMessagesLogsListError(t *testing.T) {
	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	p := newPluginWithConfig(api, &configuration{PollIntervalSeconds: 30, MaxAttempts: 3})

	api.On("KVList", 0, listPageSize).Return(([]string)(nil), model.NewAppError("KVList", "fail", nil, "", 500))
	api.On("LogError", mock.Anything, mock.Anything, mock.Anything).Return()

	p.sendDueMessages()
}

// guard against compiler removing the errors import on refactor
var _ = errors.New

// require import used
var _ = require.NoError
