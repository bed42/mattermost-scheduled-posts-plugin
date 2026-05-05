package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mattermost/mattermost-server/v6/model"
	"github.com/mattermost/mattermost-server/v6/plugin"
)

const commandTrigger = "schedule"

func (p *Plugin) scheduleCommand() *model.Command {
	cmd := &model.Command{
		Trigger:          commandTrigger,
		AutoComplete:     true,
		AutoCompleteDesc: "Schedule a message to be sent later",
		AutoCompleteHint: `"message" "YYYY-MM-DD HH:MM" [timezone]`,
		DisplayName:      "Schedule",
		Description:      "Schedule messages to be sent at a future time",
	}

	ac := model.NewAutocompleteData(commandTrigger, `"message" "YYYY-MM-DD HH:MM" [timezone]`, "Schedule a message to be sent later")

	list := model.NewAutocompleteData("list", "", "List your pending scheduled messages")
	ac.AddCommand(list)

	cancel := model.NewAutocompleteData("cancel", "<id>", "Cancel a pending scheduled message")
	cancel.AddTextArgument("ID of the scheduled message to cancel", "<id>", "")
	ac.AddCommand(cancel)

	help := model.NewAutocompleteData("help", "", "Show usage")
	ac.AddCommand(help)

	cmd.AutocompleteData = ac
	return cmd
}

func (p *Plugin) ExecuteCommand(_ *plugin.Context, args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	rawArgs := strings.TrimSpace(strings.TrimPrefix(args.Command, "/"+commandTrigger))
	tokens := parseCommandArgs(rawArgs)

	if len(tokens) == 0 {
		return ephemeral(usageText()), nil
	}

	switch strings.ToLower(tokens[0]) {
	case "help":
		return ephemeral(usageText()), nil
	case "list":
		return p.handleListCommand(args)
	case "cancel":
		if len(tokens) < 2 {
			return ephemeral("Usage: `/schedule cancel <id>`"), nil
		}
		return p.handleCancelCommand(args, tokens[1])
	default:
		return p.handleCreateCommand(args, tokens)
	}
}

func (p *Plugin) handleCreateCommand(args *model.CommandArgs, tokens []string) (*model.CommandResponse, *model.AppError) {
	if len(tokens) < 2 {
		return ephemeral("Usage: `/schedule \"message\" \"YYYY-MM-DD HH:MM\" [timezone] [repeat=...] [count=N|until=YYYY-MM-DD]`"), nil
	}

	message := tokens[0]
	timeStr := tokens[1]
	tz := ""
	repeat := ""
	until := ""
	count := 0

	// Positional arg 3, if present and not a key=value flag, is the timezone.
	startFlags := 2
	if len(tokens) >= 3 && !strings.Contains(tokens[2], "=") {
		tz = tokens[2]
		startFlags = 3
	}

	for _, tok := range tokens[startFlags:] {
		key, val, ok := strings.Cut(tok, "=")
		if !ok {
			return ephemeral("Unexpected argument: `" + tok + "`. Expected `key=value` (repeat, until, count)."), nil
		}
		switch strings.ToLower(key) {
		case "repeat":
			repeat = strings.ToLower(val)
		case "until":
			until = val
		case "count":
			n, err := strconv.Atoi(val)
			if err != nil || n < 1 {
				return ephemeral("`count=` must be a positive integer."), nil
			}
			count = n
		default:
			return ephemeral("Unknown flag: `" + key + "`."), nil
		}
	}

	if tz == "" {
		tz = p.userOrDefaultTimezone(args.UserId)
	}

	if strings.TrimSpace(message) == "" {
		return ephemeral("Message cannot be empty."), nil
	}

	sendAt, err := parseScheduleTime(timeStr, tz)
	if err != nil {
		return ephemeral("Could not parse time: " + err.Error()), nil
	}
	if err := validateSendAt(sendAt, time.Now().UTC()); err != nil {
		return ephemeral(err.Error()), nil
	}

	endsMode := ""
	switch {
	case until != "" && count != 0:
		return ephemeral("Specify either `until=` or `count=`, not both."), nil
	case until != "":
		endsMode = EndsOn
	case count != 0:
		endsMode = EndsAfter
	}
	if err := validateRecurrence(repeat, endsMode, count, until != ""); err != nil {
		return ephemeral(err.Error()), nil
	}

	msg := &ScheduledMessage{
		ID:        model.NewId(),
		ChannelID: args.ChannelId,
		UserID:    args.UserId,
		Message:   message,
		SendAt:    sendAt.UnixMilli(),
		CreatedAt: time.Now().UTC().UnixMilli(),
		Status:    StatusPending,
		Timezone:  tz,
		Repeat:    repeat,
	}
	if repeat != "" {
		msg.EndsMode = endsMode
		switch endsMode {
		case EndsOn:
			endsAt, err := parseScheduleTime(until+" 23:59", tz)
			if err != nil {
				return ephemeral("Could not parse `until` date: " + err.Error()), nil
			}
			msg.EndsAt = endsAt.UnixMilli()
		case EndsAfter:
			msg.EndsAfter = count
		}
	}
	if err := saveMessage(p.API, msg); err != nil {
		p.API.LogError("failed to save scheduled message", "err", err.Error())
		return ephemeral("Failed to save scheduled message."), nil
	}

	return ephemeral(fmt.Sprintf("Scheduled message `%s` — first send %s%s.",
		msg.ID, formatSendAt(msg.SendAt, tz), repeatSuffix(msg))), nil
}

// repeatSuffix renders a short " (repeats weekly until ...)" hint after the
// initial send time in the create-confirmation message.
func repeatSuffix(msg *ScheduledMessage) string {
	if msg.Repeat == "" {
		return ""
	}
	parts := []string{"repeats " + msg.Repeat}
	switch msg.EndsMode {
	case EndsOn:
		parts = append(parts, "until "+formatSendAt(msg.EndsAt, msg.Timezone))
	case EndsAfter:
		parts = append(parts, fmt.Sprintf("for %d occurrences", msg.EndsAfter))
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

func (p *Plugin) handleListCommand(args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	msgs, err := listMessagesForUser(p.API, args.UserId)
	if err != nil {
		p.API.LogError("failed to list scheduled messages", "err", err.Error())
		return ephemeral("Failed to load scheduled messages."), nil
	}
	if len(msgs) == 0 {
		return ephemeral("You have no pending scheduled messages."), nil
	}

	sort.Slice(msgs, func(i, j int) bool { return msgs[i].SendAt < msgs[j].SendAt })

	defaultTz := p.userOrDefaultTimezone(args.UserId)
	var sb strings.Builder
	sb.WriteString("**Your scheduled messages**\n\n| ID | When | Repeat | Status | Message |\n|---|---|---|---|---|\n")
	for _, m := range msgs {
		tz := m.Timezone
		if tz == "" {
			tz = defaultTz
		}
		msg := strings.ReplaceAll(m.Message, "|", `\|`)
		msg = backtickURLs(msg)
		fmt.Fprintf(&sb, "| `%s` | %s | %s | %s | %s |\n",
			m.ID,
			formatSendAt(m.SendAt, tz),
			repeatSummary(m),
			m.Status,
			truncate(msg, 80),
		)
	}
	return ephemeral(sb.String()), nil
}

// repeatSummary renders a compact recurrence description for the list view.
// Returns "—" for one-offs.
func repeatSummary(msg *ScheduledMessage) string {
	if msg.Repeat == "" {
		return "—"
	}
	switch msg.EndsMode {
	case EndsOn:
		return fmt.Sprintf("%s until %s", msg.Repeat, formatSendAt(msg.EndsAt, msg.Timezone))
	case EndsAfter:
		left := msg.EndsAfter - msg.Occurrences
		if left < 0 {
			left = 0
		}
		return fmt.Sprintf("%s · %d left", msg.Repeat, left)
	}
	return msg.Repeat
}

func (p *Plugin) handleCancelCommand(args *model.CommandArgs, msgID string) (*model.CommandResponse, *model.AppError) {
	existing, err := loadMessage(p.API, args.UserId, msgID)
	if err != nil {
		p.API.LogError("failed to load scheduled message", "err", err.Error())
		return ephemeral("Failed to cancel."), nil
	}
	if existing == nil {
		return ephemeral("No scheduled message found with that id."), nil
	}
	if err := deleteMessage(p.API, args.UserId, msgID); err != nil {
		p.API.LogError("failed to delete scheduled message", "err", err.Error())
		return ephemeral("Failed to cancel."), nil
	}
	return ephemeral("Cancelled scheduled message `" + msgID + "`."), nil
}

func usageText() string {
	return strings.Join([]string{
		"**Schedule a message**",
		"",
		"`/schedule \"message\" \"YYYY-MM-DD HH:MM\" [timezone] [repeat=...] [count=N|until=YYYY-MM-DD]`",
		"`/schedule list` — list your pending messages",
		"`/schedule cancel <id>` — cancel a pending message",
		"",
		"Repeat values: `daily`, `weekdays`, `weekly`, `fortnightly`, `monthly`, `yearly`.",
		"Examples:",
		"`/schedule \"stand-up\" \"2026-04-30 09:00\" Australia/Sydney repeat=weekdays count=20`",
		"`/schedule \"monthly invoice\" \"2026-05-01 10:00\" UTC repeat=monthly until=2026-12-31`",
		"",
		"Tip: click the clock icon in the channel header for a calendar UI.",
	}, "\n")
}

// userOrDefaultTimezone returns the user's Mattermost-configured timezone
// (automatic if enabled, else manual). If the user has none, the plugin's
// DefaultTimezone setting is used; UTC as a final fallback.
func (p *Plugin) userOrDefaultTimezone(userID string) string {
	if user, appErr := p.API.GetUser(userID); appErr == nil && user != nil {
		tz := user.Timezone
		if tz != nil {
			if tz["useAutomaticTimezone"] == "true" && tz["automaticTimezone"] != "" {
				return tz["automaticTimezone"]
			}
			if tz["manualTimezone"] != "" {
				return tz["manualTimezone"]
			}
		}
	}
	if cfg := p.getConfiguration().DefaultTimezone; cfg != "" {
		return cfg
	}
	return "UTC"
}

func ephemeral(text string) *model.CommandResponse {
	return &model.CommandResponse{
		ResponseType: model.CommandResponseTypeEphemeral,
		Text:         text,
	}
}
