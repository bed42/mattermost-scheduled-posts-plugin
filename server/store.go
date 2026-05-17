package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-server/v6/model"
	"github.com/mattermost/mattermost-server/v6/plugin"
	"github.com/pkg/errors"
)

const (
	StatusPending   = "pending"
	StatusSent      = "sent"
	StatusFailed    = "failed"
	StatusCompleted = "completed"

	RepeatNone        = ""
	RepeatDaily       = "daily"
	RepeatWeekdays    = "weekdays"
	RepeatWeekly      = "weekly"
	RepeatFortnightly = "fortnightly"
	RepeatMonthly     = "monthly"
	RepeatYearly      = "yearly"

	EndsNever = "never"
	EndsOn    = "on"
	EndsAfter = "after"

	keyPrefix    = "scheduled_"
	lockPrefix   = "lock_"
	listPageSize = 100
)

type ScheduledMessage struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	UserID    string `json:"user_id"`
	Message   string `json:"message"`
	SendAt    int64  `json:"send_at"`
	CreatedAt int64  `json:"created_at"`
	Status    string `json:"status"`
	Attempts  int    `json:"attempts"`
	LastError string `json:"last_error,omitempty"`

	// Recurrence (all omitempty so existing rows deserialise as zero-values =
	// one-off, identical to the pre-recurrence behaviour).
	Repeat      string `json:"repeat,omitempty"`
	Timezone    string `json:"tz,omitempty"`
	EndsMode    string `json:"ends_mode,omitempty"`
	EndsAt      int64  `json:"ends_at,omitempty"`
	EndsAfter   int    `json:"ends_after,omitempty"`
	Occurrences int    `json:"occurrences,omitempty"`

	// Random fire window: when WindowMs > 0 the actual fire instant is FireAt,
	// chosen randomly in [SendAt, SendAt+WindowMs). SendAt stays as the
	// window-start anchor so nextOccurrence re-anchors recurring schedules.
	WindowMs int64 `json:"window_ms,omitempty"`
	FireAt   int64 `json:"fire_at,omitempty"`

	// Multi-message rotation (recurring only). Messages is the rotation pool;
	// MessageCycle is the current shuffled order of indices into Messages,
	// MessageCyclePos is the next index within the cycle to send.
	// LastSentIndex remembers the previous occurrence's index so a new cycle
	// can avoid starting with the message that just fired.
	Messages        []string `json:"messages,omitempty"`
	MessageCycle    []int    `json:"message_cycle,omitempty"`
	MessageCyclePos int      `json:"message_cycle_pos,omitempty"`
	LastSentIndex   *int     `json:"last_sent_index,omitempty"`
}

// effectiveFireAt is the moment the scheduler should actually post. For
// windowed schedules this is the per-occurrence randomised FireAt; otherwise
// (and for legacy records) it falls back to SendAt.
func (m *ScheduledMessage) effectiveFireAt() int64 {
	if m.FireAt > 0 {
		return m.FireAt
	}
	return m.SendAt
}

func messageKey(userID, msgID string) string {
	return fmt.Sprintf("%s%s_%s", keyPrefix, userID, msgID)
}

func userPrefix(userID string) string {
	return fmt.Sprintf("%s%s_", keyPrefix, userID)
}

func parseUserAndMessageID(key string) (userID, msgID string, ok bool) {
	if !strings.HasPrefix(key, keyPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(key, keyPrefix)
	user, msg, found := strings.Cut(rest, "_")
	if !found {
		return "", "", false
	}
	return user, msg, true
}

func saveMessage(api plugin.API, msg *ScheduledMessage) error {
	if msg.ID == "" || msg.UserID == "" {
		return errors.New("message id and user id are required")
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return errors.Wrap(err, "failed to marshal message")
	}
	if appErr := api.KVSet(messageKey(msg.UserID, msg.ID), data); appErr != nil {
		return errors.Wrap(appErr, "failed to save message")
	}
	return nil
}

func loadMessage(api plugin.API, userID, msgID string) (*ScheduledMessage, error) {
	data, appErr := api.KVGet(messageKey(userID, msgID))
	if appErr != nil {
		return nil, errors.Wrap(appErr, "failed to load message")
	}
	if data == nil {
		return nil, nil
	}
	var msg ScheduledMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal message")
	}
	return &msg, nil
}

func deleteMessage(api plugin.API, userID, msgID string) error {
	if appErr := api.KVDelete(messageKey(userID, msgID)); appErr != nil {
		return errors.Wrap(appErr, "failed to delete message")
	}
	return nil
}

func listMessagesForUser(api plugin.API, userID string) ([]*ScheduledMessage, error) {
	prefix := userPrefix(userID)
	return listMessagesWithPrefix(api, prefix)
}

func listAllPendingMessages(api plugin.API) ([]*ScheduledMessage, error) {
	return listMessagesWithPrefix(api, keyPrefix)
}

func listMessagesWithPrefix(api plugin.API, prefix string) ([]*ScheduledMessage, error) {
	var messages []*ScheduledMessage
	page := 0
	for {
		keys, appErr := api.KVList(page, listPageSize)
		if appErr != nil {
			return nil, errors.Wrap(appErr, "failed to list keys")
		}
		if len(keys) == 0 {
			break
		}
		for _, key := range keys {
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			data, appErr := api.KVGet(key)
			if appErr != nil {
				api.LogWarn("failed to load scheduled message", "key", key, "err", appErr.Error())
				continue
			}
			if data == nil {
				continue
			}
			var msg ScheduledMessage
			if err := json.Unmarshal(data, &msg); err != nil {
				api.LogWarn("failed to unmarshal scheduled message", "key", key, "err", err.Error())
				continue
			}
			messages = append(messages, &msg)
		}
		if len(keys) < listPageSize {
			break
		}
		page++
	}
	return messages, nil
}

// acquireSendLock attempts to grab a per-message lock so only one cluster node
// sends a given scheduled message. Returns true if the lock was acquired.
func acquireSendLock(api plugin.API, msgID string) (bool, error) {
	key := lockPrefix + msgID
	ok, appErr := api.KVSetWithOptions(key, []byte("1"), model.PluginKVSetOptions{
		Atomic:          true,
		OldValue:        nil,
		ExpireInSeconds: 60,
	})
	if appErr != nil {
		return false, errors.Wrap(appErr, "failed to acquire send lock")
	}
	return ok, nil
}

func releaseSendLock(api plugin.API, msgID string) {
	_ = api.KVDelete(lockPrefix + msgID)
}
