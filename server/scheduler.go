package main

import (
	"fmt"
	"strings"
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
		if msg.Status == StatusCompleted && msg.effectiveFireAt() < now-completedRetentionMs {
			if err := deleteMessage(p.API, msg.UserID, msg.ID); err != nil {
				p.API.LogWarn("scheduler: gc of completed message failed", "id", msg.ID, "err", err.Error())
			}
			continue
		}
		if msg.Status != StatusPending {
			continue
		}
		if msg.effectiveFireAt() > now {
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

	body, sentIdx := pickMessageBody(current)
	targets := current.Targets()
	if len(targets) == 0 {
		p.API.LogError("scheduler: message has no target channels, dropping", "id", current.ID)
		_ = deleteMessage(p.API, current.UserID, current.ID)
		return
	}

	var failures []postFailure
	successes := 0
	for _, channelID := range targets {
		post := &model.Post{
			ChannelId: channelID,
			UserId:    current.UserID,
			Message:   body,
		}
		if _, appErr := p.API.CreatePost(post); appErr != nil {
			failures = append(failures, postFailure{channelID: channelID, err: appErr.Error()})
			continue
		}
		successes++
	}

	// All targets failed → treat as a full dispatch failure and use the
	// existing retry path. The retry will hit every channel again, which is
	// safe because none received the message this round.
	if successes == 0 {
		appErrMsg := joinFailures(failures)
		current.Attempts++
		current.LastError = appErrMsg
		if current.Attempts >= maxAttempts {
			if current.Repeat != "" {
				// Recurring: skip this occurrence rather than killing the whole series.
				p.skipFailedOccurrence(current, appErrMsg)
				return
			}
			current.Status = StatusFailed
			p.API.LogError("scheduler: giving up on message",
				"id", current.ID, "attempts", current.Attempts, "err", appErrMsg)
		} else {
			p.API.LogWarn("scheduler: send failed, will retry",
				"id", current.ID, "attempts", current.Attempts, "err", appErrMsg)
		}
		if saveErr := saveMessage(p.API, current); saveErr != nil {
			p.API.LogError("scheduler: failed to persist failure state", "err", saveErr.Error())
		}
		return
	}

	// At least one channel received the message. Log any per-target failures
	// individually and treat the dispatch as complete — retrying would
	// duplicate posts in the channels that already succeeded.
	for _, f := range failures {
		p.API.LogError("scheduler: per-target send failed (not retrying)",
			"id", current.ID, "channel_id", f.channelID, "err", f.err)
	}

	// Send succeeded.
	if current.Repeat == "" {
		if err := deleteMessage(p.API, current.UserID, current.ID); err != nil {
			p.API.LogError("scheduler: failed to delete sent message", "id", current.ID, "err", err.Error())
		}
		return
	}
	p.advanceRecurring(current, sentIdx)
}

// advanceRecurring rolls a successfully-sent recurring message to its next
// occurrence, or marks it completed if the series has ended. When sentIdx
// is non-negative the rotation pos and LastSentIndex are updated for the
// just-sent message.
func (p *Plugin) advanceRecurring(msg *ScheduledMessage, sentIdx int) {
	msg.Occurrences++
	if sentIdx >= 0 {
		idx := sentIdx
		msg.LastSentIndex = &idx
		msg.MessageCyclePos++
	}
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
		msg.FireAt = 0
		if err := saveMessage(p.API, msg); err != nil {
			p.API.LogError("scheduler: failed to mark series completed", "id", msg.ID, "err", err.Error())
		}
		return
	}
	msg.SendAt = next.UnixMilli()
	msg.FireAt = randomFireAt(msg.SendAt, msg.WindowMs)
	msg.Attempts = 0
	msg.LastError = ""
	if err := saveMessage(p.API, msg); err != nil {
		p.API.LogError("scheduler: failed to roll forward recurring message", "id", msg.ID, "err", err.Error())
	}
}

// postFailure records a single per-channel CreatePost failure during a
// multi-target dispatch.
type postFailure struct {
	channelID string
	err       string
}

// joinFailures collapses per-channel failure errors into a single string for
// persistence in LastError. Order is preserved so users can map errors back to
// their target order. Each entry is "channelID: err".
func joinFailures(failures []postFailure) string {
	parts := make([]string, 0, len(failures))
	for _, f := range failures {
		parts = append(parts, fmt.Sprintf("%s: %s", f.channelID, f.err))
	}
	return strings.Join(parts, "; ")
}

// randomFireAt returns sendAt + a random offset in [0, windowMs) when
// windowMs > 0, or 0 otherwise (signalling "no random window — use SendAt").
func randomFireAt(sendAt, windowMs int64) int64 {
	if windowMs <= 0 {
		return 0
	}
	return sendAt + randInt63n(windowMs)
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
		msg.FireAt = 0
		if err := saveMessage(p.API, msg); err != nil {
			p.API.LogError("scheduler: failed to complete after skip", "id", msg.ID, "err", err.Error())
		}
		return
	}
	msg.SendAt = next.UnixMilli()
	msg.FireAt = randomFireAt(msg.SendAt, msg.WindowMs)
	msg.Attempts = 0
	msg.LastError = "skipped: " + lastErr
	if err := saveMessage(p.API, msg); err != nil {
		p.API.LogError("scheduler: failed to skip occurrence", "id", msg.ID, "err", err.Error())
	}
}
