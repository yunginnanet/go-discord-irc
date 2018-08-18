package webhooks

import (
	"strings"

	"github.com/hashicorp/go-multierror"

	"github.com/bwmarrin/discordgo"
	"github.com/pkg/errors"
)

// Package webhooks provides functionality for displaying arbitrary
// webhook messages on Discord.
//
// Existing webhooks are used for messages sent, and if necessary,
// new webhooks are created to ensure messages in multiple popular channels
// don't cause messages to be registered as new users.
//

// A Transmitter represents a message manager instance for a single guild.
type Transmitter struct {
	session *discordgo.Session
	guild   string
	prefix  string

	webhooks []*discordgo.Webhook
}

// NewTransmitter returns a new Transmitter given a Discord session, guild ID, and webhook prefix.
func NewTransmitter(session *discordgo.Session, guild string, prefix string) (*Transmitter, error) {
	// Get all existing webhooks
	hooks, err := session.GuildWebhooks(guild)

	// Check to make sure we have permissions
	if err != nil {
		restErr := err.(*discordgo.RESTError)
		if restErr.Message != nil && restErr.Message.Code == 50013 {
			return nil, errors.Wrap(err, "the 'Manage Webhooks' permission is required")
		}

		return nil, errors.Wrap(err, "could not get webhooks")
	}

	// Delete existing webhooks with the same prefix
	for _, wh := range hooks {
		if strings.HasPrefix(wh.Name, prefix) {
			if err := session.WebhookDelete(wh.ID); err != nil {
				return nil, errors.Errorf("Could not delete webhook %s (\"%s\")", wh.ID, wh.Name)
			}
		}
	}

	return &Transmitter{
		session: session,
		guild:   guild,
		prefix:  prefix,

		webhooks: []*discordgo.Webhook{},
	}, nil
}

// Close immediately stops all active webhook timers and deletes webhooks.
func (t *Transmitter) Close() error {
	var result error

	// Delete all the webhooks
	for _, webhook := range t.webhooks {
		err := t.session.WebhookDelete(webhook.ID)
		if err != nil {
			multierror.Append(result, errors.Wrapf(err, "could not remove hook %s", webhook.ID))
		}
	}

	return result
}
