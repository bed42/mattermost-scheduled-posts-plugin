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
}

func (p *Plugin) apiCreate(w http.ResponseWriter, r *http.Request, userID string) {
	var body createPayload
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	body.Message = strings.TrimSpace(body.Message)

	if body.ChannelID == "" {
		http.Error(w, "channel_id is required", http.StatusBadRequest)
		return
	}
	if body.Message == "" {
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

	tz := body.Timezone
	if tz == "" {
		tz = p.userOrDefaultTimezone(userID)
	}

	msg := &ScheduledMessage{
		ID:        model.NewId(),
		ChannelID: body.ChannelID,
		UserID:    userID,
		Message:   body.Message,
		SendAt:    sendAt.UnixMilli(),
		CreatedAt: time.Now().UTC().UnixMilli(),
		Status:    StatusPending,
		Timezone:  tz,
		Repeat:    body.Repeat,
	}
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
