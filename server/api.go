package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/mattermost/mattermost-server/v6/model"
)

const userIDHeader = "Mattermost-User-Id"

func (p *Plugin) handleHTTP(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get(userIDHeader)
	if userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/list":
		p.apiList(w, r, userID)
	case r.Method == http.MethodPost && r.URL.Path == "/api/create":
		p.apiCreate(w, r, userID)
	case r.Method == http.MethodDelete && r.URL.Path == "/api/cancel":
		p.apiCancel(w, r, userID)
	case r.Method == http.MethodPatch && r.URL.Path == "/api/update":
		p.apiUpdate(w, r, userID)
	default:
		http.NotFound(w, r)
	}
}

func (p *Plugin) apiList(w http.ResponseWriter, _ *http.Request, userID string) {
	msgs, err := listMessagesForUser(p.API, userID)
	if err != nil {
		p.API.LogError("api: list failed", "err", err.Error())
		http.Error(w, "list failed", http.StatusInternalServerError)
		return
	}
	if msgs == nil {
		msgs = []*ScheduledMessage{}
	}
	writeJSON(w, http.StatusOK, msgs)
}

type createPayload struct {
	ChannelID string `json:"channel_id"`
	Message   string `json:"message"`
	SendAt    string `json:"send_at"`  // ISO8601
	Timezone  string `json:"timezone"` // optional, used for parsing if SendAt has no offset

	Repeat    string `json:"repeat,omitempty"`
	EndsMode  string `json:"ends_mode,omitempty"`
	EndsOn    string `json:"ends_on,omitempty"` // YYYY-MM-DD interpreted in Timezone
	EndsAfter int    `json:"ends_after,omitempty"`

	WindowMs int64    `json:"window_ms,omitempty"`
	Messages []string `json:"messages,omitempty"`
}

func (p *Plugin) apiCreate(w http.ResponseWriter, r *http.Request, userID string) {
	var body createPayload
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	body.Message = strings.TrimSpace(body.Message)
	body.Messages = trimMessages(body.Messages)

	if body.ChannelID == "" {
		http.Error(w, "channel_id is required", http.StatusBadRequest)
		return
	}
	if body.Message == "" && len(body.Messages) == 0 {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}
	if !p.userCanPostInChannel(userID, body.ChannelID) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	sendAt, err := parseScheduleTime(body.SendAt, body.Timezone)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := validateSendAt(sendAt, time.Now().UTC()); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := validateRecurrence(body.Repeat, body.EndsMode, body.EndsAfter, body.EndsOn != ""); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := validateExtras(body.Repeat, body.WindowMs, body.Messages); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	tz := body.Timezone
	if tz == "" {
		tz = p.userOrDefaultTimezone(userID)
	}

	primaryMessage := body.Message
	if len(body.Messages) > 0 {
		primaryMessage = body.Messages[0]
	}

	msg := &ScheduledMessage{
		ID:        model.NewId(),
		ChannelID: body.ChannelID,
		UserID:    userID,
		Message:   primaryMessage,
		SendAt:    sendAt.UnixMilli(),
		CreatedAt: time.Now().UTC().UnixMilli(),
		Status:    StatusPending,
		Timezone:  tz,
		Repeat:    body.Repeat,
		WindowMs:  body.WindowMs,
	}
	if len(body.Messages) > 0 {
		msg.Messages = body.Messages
	}
	msg.FireAt = randomFireAt(msg.SendAt, msg.WindowMs)
	if body.Repeat != "" {
		msg.EndsMode = body.EndsMode
		if body.EndsMode == EndsAfter {
			msg.EndsAfter = body.EndsAfter
		}
		if body.EndsMode == EndsOn {
			endsAt, err := parseScheduleTime(body.EndsOn+" 23:59", tz)
			if err != nil {
				http.Error(w, "invalid ends_on: "+err.Error(), http.StatusBadRequest)
				return
			}
			msg.EndsAt = endsAt.UnixMilli()
		}
	}

	if err := saveMessage(p.API, msg); err != nil {
		p.API.LogError("api: save failed", "err", err.Error())
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusCreated, msg)
}

type updatePayload struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	Message   string `json:"message"`
	SendAt    string `json:"send_at"`
	Timezone  string `json:"timezone"`

	Repeat    string `json:"repeat,omitempty"`
	EndsMode  string `json:"ends_mode,omitempty"`
	EndsOn    string `json:"ends_on,omitempty"`
	EndsAfter int    `json:"ends_after,omitempty"`

	WindowMs int64    `json:"window_ms,omitempty"`
	Messages []string `json:"messages,omitempty"`
}

// apiUpdate edits a pending scheduled message in place. ID, CreatedAt, Status,
// Attempts, LastError, and Occurrences are preserved — editing a mid-series
// recurring schedule does not reset its occurrence counter.
func (p *Plugin) apiUpdate(w http.ResponseWriter, r *http.Request, userID string) {
	var body updatePayload
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if body.ID == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	body.Message = strings.TrimSpace(body.Message)
	body.Messages = trimMessages(body.Messages)
	if body.Message == "" && len(body.Messages) == 0 {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}
	if body.ChannelID == "" {
		http.Error(w, "channel_id is required", http.StatusBadRequest)
		return
	}
	if !p.userCanPostInChannel(userID, body.ChannelID) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	existing, err := loadMessage(p.API, userID, body.ID)
	if err != nil {
		http.Error(w, "load failed", http.StatusInternalServerError)
		return
	}
	if existing == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if existing.Status != StatusPending {
		http.Error(w, "only pending messages can be edited", http.StatusConflict)
		return
	}

	sendAt, err := parseScheduleTime(body.SendAt, body.Timezone)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := validateSendAt(sendAt, time.Now().UTC()); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := validateRecurrence(body.Repeat, body.EndsMode, body.EndsAfter, body.EndsOn != ""); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := validateExtras(body.Repeat, body.WindowMs, body.Messages); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	tz := body.Timezone
	if tz == "" {
		tz = p.userOrDefaultTimezone(userID)
	}

	primaryMessage := body.Message
	if len(body.Messages) > 0 {
		primaryMessage = body.Messages[0]
	}

	existing.ChannelID = body.ChannelID
	existing.Message = primaryMessage
	existing.SendAt = sendAt.UnixMilli()
	existing.Timezone = tz
	existing.Repeat = body.Repeat
	existing.WindowMs = body.WindowMs
	existing.FireAt = randomFireAt(existing.SendAt, existing.WindowMs)

	// Reset rotation when the message list changes (also when Messages
	// goes from non-empty to empty, e.g. user removed extras).
	if !equalStrings(existing.Messages, body.Messages) {
		existing.Messages = body.Messages
		existing.MessageCycle = nil
		existing.MessageCyclePos = 0
		existing.LastSentIndex = nil
	}

	existing.EndsMode = ""
	existing.EndsAt = 0
	existing.EndsAfter = 0
	if body.Repeat != "" {
		existing.EndsMode = body.EndsMode
		if body.EndsMode == EndsAfter {
			existing.EndsAfter = body.EndsAfter
		}
		if body.EndsMode == EndsOn {
			endsAt, err := parseScheduleTime(body.EndsOn+" 23:59", tz)
			if err != nil {
				http.Error(w, "invalid ends_on: "+err.Error(), http.StatusBadRequest)
				return
			}
			existing.EndsAt = endsAt.UnixMilli()
		}
	}

	if err := saveMessage(p.API, existing); err != nil {
		p.API.LogError("api: update failed", "err", err.Error())
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, existing)
}

type cancelPayload struct {
	ID string `json:"id"`
}

func (p *Plugin) apiCancel(w http.ResponseWriter, r *http.Request, userID string) {
	var body cancelPayload
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if body.ID == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}

	// loadMessage scopes by userID, so a user can only cancel their own messages.
	existing, err := loadMessage(p.API, userID, body.ID)
	if err != nil {
		http.Error(w, "load failed", http.StatusInternalServerError)
		return
	}
	if existing == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err := deleteMessage(p.API, userID, body.ID); err != nil {
		http.Error(w, "delete failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (p *Plugin) userCanPostInChannel(userID, channelID string) bool {
	_, appErr := p.API.GetChannelMember(channelID, userID)
	return appErr == nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
