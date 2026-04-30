package main

import (
	"time"

	"github.com/mattermost/mattermost-server/v6/model"
)

func (p *Plugin) runScheduler() {
	defer close(p.doneCh)

	cfg := p.getConfiguration()
	interval := time.Duration(cfg.PollIntervalSeconds) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Run once at startup so freshly-due messages are sent without waiting an interval.
	p.sendDueMessages()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			// Re-read config in case PollIntervalSeconds changed.
			newInterval := time.Duration(p.getConfiguration().PollIntervalSeconds) * time.Second
			if newInterval != interval {
				interval = newInterval
				ticker.Reset(interval)
			}
			p.sendDueMessages()
		}
	}
}

// completedRetentionMs — completed recurring series stay around for a week
// so users can confirm "yes the series finished" in the list view.
const completedRetentionMs int64 = 7 * 24 * 60 * 60 * 1000

func (p *Plugin) sendDueMessages() {
	msgs, err := listAllPendingMessages(p.API)
	if err != nil {
		p.API.LogError("scheduler: failed to list messages", "err", err.Error())
		return
	}
	now := time.Now().UTC().UnixMilli()
	maxAttempts := p.getConfiguration().MaxAttempts

	for _, msg := range msgs {
		// GC completed series after the retention window.
		if msg.Status == StatusCompleted && msg.SendAt < now-completedRetentionMs {
			if err := deleteMessage(p.API, msg.UserID, msg.ID); err != nil {
				p.API.LogWarn("scheduler: gc of completed message failed", "id", msg.ID, "err", err.Error())
			}
			continue
		}
		if msg.Status != StatusPending {
			continue
		}
		if msg.SendAt > now {
			continue
		}
		p.dispatch(msg, maxAttempts)
	}
}

func (p *Plugin) dispatch(msg *ScheduledMessage, maxAttempts int) {
	got, err := acquireSendLock(p.API, msg.ID)
	if err != nil {
		p.API.LogWarn("scheduler: lock error", "id", msg.ID, "err", err.Error())
		return
	}
	if !got {
		// Another node owns this dispatch.
		return
	}
	defer releaseSendLock(p.API, msg.ID)

	// Re-load after locking — another node may have already processed this.
	current, err := loadMessage(p.API, msg.UserID, msg.ID)
	if err != nil || current == nil || current.Status != StatusPending {
		return
	}

	post := &model.Post{
		ChannelId: current.ChannelID,
		UserId:    current.UserID,
		Message:   current.Message,
	}
	if _, appErr := p.API.CreatePost(post); appErr != nil {
		current.Attempts++
		current.LastError = appErr.Error()
		if current.Attempts >= maxAttempts {
			if current.Repeat != "" {
				// Recurring: skip this occurrence rather than killing the whole series.
				p.skipFailedOccurrence(current, appErr.Error())
				return
			}
			current.Status = StatusFailed
			p.API.LogError("scheduler: giving up on message",
				"id", current.ID, "attempts", current.Attempts, "err", appErr.Error())
		} else {
			p.API.LogWarn("scheduler: send failed, will retry",
				"id", current.ID, "attempts", current.Attempts, "err", appErr.Error())
		}
		if saveErr := saveMessage(p.API, current); saveErr != nil {
			p.API.LogError("scheduler: failed to persist failure state", "err", saveErr.Error())
		}
		return
	}

	// Send succeeded.
	if current.Repeat == "" {
		if err := deleteMessage(p.API, current.UserID, current.ID); err != nil {
			p.API.LogError("scheduler: failed to delete sent message", "id", current.ID, "err", err.Error())
		}
		return
	}
	p.advanceRecurring(current)
}

// advanceRecurring rolls a successfully-sent recurring message to its next
// occurrence, or marks it completed if the series has ended.
func (p *Plugin) advanceRecurring(msg *ScheduledMessage) {
	msg.Occurrences++
	next, err := nextOccurrence(time.UnixMilli(msg.SendAt), msg.Repeat, msg.Timezone)
	if err != nil {
		// Bad recurrence (shouldn't happen — validated at create time) — log & delete.
		p.API.LogError("scheduler: invalid recurrence, dropping", "id", msg.ID, "err", err.Error())
		_ = deleteMessage(p.API, msg.UserID, msg.ID)
		return
	}
	if seriesEnded(msg, next) {
		msg.Status = StatusCompleted
		msg.Attempts = 0
		msg.LastError = ""
		if err := saveMessage(p.API, msg); err != nil {
			p.API.LogError("scheduler: failed to mark series completed", "id", msg.ID, "err", err.Error())
		}
		return
	}
	msg.SendAt = next.UnixMilli()
	msg.Attempts = 0
	msg.LastError = ""
	if err := saveMessage(p.API, msg); err != nil {
		p.API.LogError("scheduler: failed to roll forward recurring message", "id", msg.ID, "err", err.Error())
	}
}

// skipFailedOccurrence advances a recurring message past an occurrence that
// hit max attempts. The occurrence count is *not* incremented (it didn't send).
func (p *Plugin) skipFailedOccurrence(msg *ScheduledMessage, lastErr string) {
	p.API.LogError("scheduler: skipping failed recurring occurrence",
		"id", msg.ID, "attempts", msg.Attempts, "err", lastErr)
	next, err := nextOccurrence(time.UnixMilli(msg.SendAt), msg.Repeat, msg.Timezone)
	if err != nil {
		p.API.LogError("scheduler: invalid recurrence on skip, dropping", "id", msg.ID, "err", err.Error())
		_ = deleteMessage(p.API, msg.UserID, msg.ID)
		return
	}
	if seriesEnded(msg, next) {
		msg.Status = StatusCompleted
		if err := saveMessage(p.API, msg); err != nil {
			p.API.LogError("scheduler: failed to complete after skip", "id", msg.ID, "err", err.Error())
		}
		return
	}
	msg.SendAt = next.UnixMilli()
	msg.Attempts = 0
	msg.LastError = "skipped: " + lastErr
	if err := saveMessage(p.API, msg); err != nil {
		p.API.LogError("scheduler: failed to skip occurrence", "id", msg.ID, "err", err.Error())
	}
}
