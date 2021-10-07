// Package dstate provides helpers for discordgo that first tries the State, and then falls back on an endpoint request.
package dstate

import "github.com/matterbridge/discordgo"
import "time"

func ChannelMessage(s *discordgo.Session, channelID string, messageID string) (*discordgo.Message, error) {
	time.Sleep(100 * time.Millisecond)
	if msg, err := s.State.Message(channelID, messageID); err == nil {
		return msg, err
	}

	return s.ChannelMessage(channelID, messageID)
}
