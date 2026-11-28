package slackintake

import "github.com/nep-ai/difqo/internal/store"

// Callback is the Slack Events API payload carried by Socket Mode or a signed webhook.
type Callback struct {
	Type          string `json:"type"`
	Challenge     string `json:"challenge"`
	EventID       string `json:"event_id"`
	AppID         string `json:"api_app_id"`
	WorkspaceID   string `json:"team_id"`
	ExternalShare bool   `json:"is_ext_shared_channel"`
	Event         struct {
		Type        string    `json:"type"`
		Channel     string    `json:"channel"`
		ChannelType string    `json:"channel_type"`
		User        string    `json:"user"`
		UserTeam    string    `json:"user_team"`
		Team        string    `json:"team"`
		BotID       string    `json:"bot_id"`
		BotProfile  *struct{} `json:"bot_profile"`
		AppID       string    `json:"app_id"`
		Subtype     string    `json:"subtype"`
		Text        string    `json:"text"`
	} `json:"event"`
}

// Goal normalizes a human DM after its transport has authenticated the payload.
// An empty goal means the event is outside the supported intake scope.
func (callback Callback) Goal(requirePrefix bool) (store.SlackBinding, string) {
	if callback.Type != "event_callback" || callback.ExternalShare {
		return store.SlackBinding{}, ""
	}
	message := callback.Event
	if (message.Team != "" && message.Team != callback.WorkspaceID) ||
		(message.UserTeam != "" && message.UserTeam != callback.WorkspaceID) {
		return store.SlackBinding{}, ""
	}
	bot := message.BotID != "" || message.AppID != "" || message.BotProfile != nil
	incoming := event{
		SlackBinding: store.SlackBinding{AppID: callback.AppID, WorkspaceID: callback.WorkspaceID,
			OperatorID: message.User, ChannelID: message.Channel},
		EventID: callback.EventID, Type: message.Type, ChannelType: message.ChannelType,
		Bot: &bot, Subtype: message.Subtype, Text: message.Text,
	}
	value, err := incoming.goal(requirePrefix)
	if err != nil {
		return store.SlackBinding{}, ""
	}
	return incoming.SlackBinding, value
}
