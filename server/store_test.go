package main

import (
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost-server/v6/model"
	"github.com/mattermost/mattermost-server/v6/plugin/plugintest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestSaveAndLoadMessage(t *testing.T) {
	api := &plugintest.API{}
	defer api.AssertExpectations(t)

	msg := &ScheduledMessage{
		ID:      "m1",
		UserID:  "u1",
		Message: "hello",
		Status:  StatusPending,
	}

	api.On("KVSet", "scheduled_u1_m1", mock.MatchedBy(func(b []byte) bool {
		var got ScheduledMessage
		return json.Unmarshal(b, &got) == nil && got.ID == "m1" && got.Message == "hello"
	})).Return(nil)

	require.NoError(t, saveMessage(api, msg))

	encoded, _ := json.Marshal(msg)
	api.On("KVGet", "scheduled_u1_m1").Return(encoded, nil)

	got, err := loadMessage(api, "u1", "m1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "hello", got.Message)
}

func TestLoadMessageNotFound(t *testing.T) {
	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	api.On("KVGet", "scheduled_u1_missing").Return(([]byte)(nil), nil)
	got, err := loadMessage(api, "u1", "missing")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestSaveMessageRequiresIDAndUser(t *testing.T) {
	api := &plugintest.API{}
	require.Error(t, saveMessage(api, &ScheduledMessage{ID: "", UserID: "u"}))
	require.Error(t, saveMessage(api, &ScheduledMessage{ID: "m", UserID: ""}))
}

func TestDeleteMessage(t *testing.T) {
	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	api.On("KVDelete", "scheduled_u1_m1").Return(nil)
	require.NoError(t, deleteMessage(api, "u1", "m1"))
}

func TestListMessagesForUserFiltersByPrefix(t *testing.T) {
	api := &plugintest.API{}
	defer api.AssertExpectations(t)

	mine := &ScheduledMessage{ID: "m1", UserID: "u1", Message: "mine"}
	other := &ScheduledMessage{ID: "m2", UserID: "u2", Message: "other"}
	mineBytes, _ := json.Marshal(mine)
	otherBytes, _ := json.Marshal(other)

	api.On("KVList", 0, listPageSize).Return([]string{
		"scheduled_u1_m1",
		"scheduled_u2_m2",
		"unrelated_key",
	}, nil)
	api.On("KVGet", "scheduled_u1_m1").Return(mineBytes, nil)
	// scheduled_u2_m2 must NOT be fetched — it doesn't match prefix.
	_ = otherBytes

	msgs, err := listMessagesForUser(api, "u1")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, "mine", msgs[0].Message)
}

func TestListAllPendingMessagesPaginates(t *testing.T) {
	api := &plugintest.API{}
	defer api.AssertExpectations(t)

	// Build a full first page (100 keys) and a partial second page (1 key).
	page1Keys := make([]string, listPageSize)
	for i := 0; i < listPageSize; i++ {
		id := "msg" + itoa(i)
		page1Keys[i] = "scheduled_u1_" + id
		body, _ := json.Marshal(&ScheduledMessage{ID: id, UserID: "u1"})
		api.On("KVGet", page1Keys[i]).Return(body, nil)
	}
	page2Keys := []string{"scheduled_u1_last"}
	lastBody, _ := json.Marshal(&ScheduledMessage{ID: "last", UserID: "u1"})
	api.On("KVGet", "scheduled_u1_last").Return(lastBody, nil)

	api.On("KVList", 0, listPageSize).Return(page1Keys, nil)
	api.On("KVList", 1, listPageSize).Return(page2Keys, nil)

	msgs, err := listAllPendingMessages(api)
	require.NoError(t, err)
	assert.Len(t, msgs, listPageSize+1)
}

func TestAcquireSendLockReturnsAtomicResult(t *testing.T) {
	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	api.On("KVSetWithOptions", "lock_m1", []byte("1"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(true, nil)
	got, err := acquireSendLock(api, "m1")
	require.NoError(t, err)
	assert.True(t, got)
}

func TestAcquireSendLockReturnsFalseWhenContended(t *testing.T) {
	api := &plugintest.API{}
	defer api.AssertExpectations(t)
	api.On("KVSetWithOptions", "lock_m1", []byte("1"), mock.AnythingOfType("model.PluginKVSetOptions")).Return(false, nil)
	got, err := acquireSendLock(api, "m1")
	require.NoError(t, err)
	assert.False(t, got)
}

// itoa avoids importing strconv just for tests.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := []byte{}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

// Confirm model.PluginKVSetOptions is the type the mock receives.
var _ = model.PluginKVSetOptions{}
