package bridge

import (
	"crypto/tls"
	"fmt"
	"os"
	"strings"
	"time"

	irc "git.tcp.direct/kayos/girc-tcpd"
	log "github.com/sirupsen/logrus"

	ircf "github.com/qaisjp/go-discord-irc/irc/format"
)

type ircListener struct {
	*irc.Client
	bridge *Bridge

	listenerCallbackIDs map[string]int
}

func newIRCListener(dib *Bridge, webIRCPass string) *ircListener {
	var ircConfig = irc.Config{
		Server:     dib.Config.IRCListenerName,
		User:       dib.Config.IRCListenerName,
		Nick:       dib.Config.IRCListenerName,
		ServerPass: dib.Config.IRCServerPass,
		Version:    "tcp.direct",
	}

	if dib.Config.Debug {
		ircConfig.Debug = os.Stdout
	}

	if !dib.Config.NoTLS {
		ircConfig.SSL = true
		ircConfig.TLSConfig = &tls.Config{
			InsecureSkipVerify: dib.Config.InsecureSkipVerify,
		}
	}

	ircClient := irc.New(ircConfig)

	// On kick, rejoin the channel
	ircClient.Handlers.Add("KICK", func(c *irc.Client, e irc.Event) {
		if e.Params[1] == c.GetNick() {
			c.Cmd.Join(e.Params[0])
		}
	})

	listener := &ircListener{ircClient, dib, make(map[string]int)}

	// Welcome event
	ircClient.Handlers.Add(irc.CONNECTED, listener.JoinChannels)

	// Called when received channel names... essentially OnJoinChannel
	ircClient.Handlers.Add("366", listener.OnJoinChannel)
	ircClient.Handlers.Add("PRIVMSG", listener.OnPrivateMessage)
	ircClient.Handlers.Add("NOTICE", listener.OnPrivateMessage)
	ircClient.Handlers.Add("CTCP_ACTION", listener.OnPrivateMessage)

	// Try to rejoin channels after authenticated with NickServ
	ircClient.Handlers.Add("900", listener.JoinChannels)

	return listener
}

func (i *ircListener) OnNickRelayToDiscord(e irc.Event) {
	// ignored hostmasks, or we're a puppet? no relay
	if i.bridge.ircManager.isIgnoredHostmask(e.Source.String()) ||
		i.isPuppetNick(e.Source.Name) ||
		i.isPuppetNick(e.Last()) {
		return
	}

	oldNick := e.Source.Name
	newNick := e.Last()

	msg := IRCMessage{
		Username: "",
		Message:  fmt.Sprintf("_%s changed their nick to %s_", oldNick, newNick),
	}

	for _, m := range i.bridge.mappings {
		channel := m.IRCChannel

		if channelObj := i.Client.LookupChannel(channel); channelObj != nil {
			if channelObj.UserIn(newNick) {
				msg.IRCChannel = channel
				i.bridge.discordMessagesChan <- msg
			}
		}
	}
}

func (i *ircListener) nickTrackPuppetQuit(e irc.Event) {
	// Protect against HostServ changing nicks or ircd's with CHGHOST/CHGIDENT or similar
	// sending us a QUIT for a puppet nick only for it to rejoin right after.
	// The puppet nick won't see a true disconnection itself and thus will still see itself
	// as connected.
	if con, ok := i.bridge.ircManager.puppetNicks[e.Source.Name]; ok && !con.Connected() {
		delete(i.bridge.ircManager.puppetNicks, e.Source.Name)
	}
}

func (i *ircListener) OnJoinQuitCallback(e irc.Event) {
	// This checks if the source of the event was from a puppet.
	if (e.Command == "KICK" && i.isPuppetNick(e.Params[1])) || i.isPuppetNick(e.Source.Name) {
		// since we replace the STQUIT callback we have to manage our puppet nicks when
		// this call back is active!
		if e.Command == "STQUIT" {
			i.nickTrackPuppetQuit(e)
		}
		return
	}

	// Ignored hostmasks
	if i.bridge.ircManager.isIgnoredHostmask(e.Source.String()) {
		return
	}

	who := e.Source.Name
	message := e.Source.Name
	id := " (" + e.Source.Ident + "@" + e.Source.Host + ") "

	switch e.Command {
	case "STJOIN":
		message += " joined" + id
	case "STPART":
		message += " left" + id
		if len(e.Params) > 1 {
			message += ": " + e.Params[1]
		}
	case "STQUIT":
		message += " quit" + id

		reason := e.Source.Name
		if len(e.Params) == 1 {
			reason = e.Params[0]
		}
		message += "Quit: " + reason
	case "KICK":
		who = e.Params[1]
		message = e.Params[1] + " was kicked by " + e.Source.Name + ": " + e.Params[2]
	}

	msg := IRCMessage{
		// IRCChannel: set on the fly
		Username: "",
		Message:  message,
	}

	if e.Command == "STQUIT" {
		// Notify channels that the user is in
		for _, m := range i.bridge.mappings {
			channel := m.IRCChannel
			channelObj := i.Client.LookupChannel(channel)
			if channelObj == nil {
				log.WithField("channel", channel).WithField("who", who).Warnln("Trying to process QUIT. Channel not found in irc listener cache.")
				continue
			}

			if channelObj.UserIn(who) {
				continue
			}
			msg.IRCChannel = channel
			i.bridge.discordMessagesChan <- msg
		}
	} else {
		msg.IRCChannel = e.Params[0]
		i.bridge.discordMessagesChan <- msg
	}
}

func (i *ircListener) DoesUserExist(user string) (exists bool) {
	exists = false
	i.Client.Cmd.SendRawf("ISON %s", user)
	i.Client.Handlers.AddTmp("303", 5*time.Second, func(c *irc.Client, e irc.Event) bool {
		if e.Params[len(e.Params)] == user {
			exists = true
		}
		return false
	})
	return
}

func (i *ircListener) SetDebugMode(debug bool) {
	// i.VerboseCallbackHandler = debug
	// i.Debug = debug
}

func (i *ircListener) JoinChannels(c *irc.Client, e irc.Event) {
	for _, mapping := range i.bridge.mappings {
		c.Cmd.Join(mapping.IRCChannel)
	}
}

func (i *ircListener) OnJoinChannel(c *irc.Client, e irc.Event) {
	log.Infof("Listener has joined IRC channel %s.", e.Params[1])
}

func (i *ircListener) isPuppetNick(nick string) bool {
	if i.GetNick() == nick {
		return true
	}
	if _, ok := i.bridge.ircManager.puppetNicks[nick]; ok {
		return true
	}
	return false
}

func (i *ircListener) OnPrivateMessage(c *irc.Client, e irc.Event) {
	// Ignore private messages
	if string(e.Params[0][0]) != "#" {
		// If you decide to extend this to respond to PMs, make sure
		// you do not respond to NOTICEs, see issue #50.
		return
	}

	if strings.TrimSpace(e.Last()) == "" || // Discord doesn't accept an empty message
		i.isPuppetNick(e.Source.Name) || // ignore msg's from our puppets
		i.bridge.ircManager.isIgnoredHostmask(e.Source.String()) || // ignored hostmasks
		i.bridge.ircManager.isFilteredIRCMessage(e.Last()) { // filtered
		return
	}

	replacements := []string{}
	for _, con := range i.bridge.ircManager.ircConnections {
		replacements = append(replacements, con.nick, "<@!"+con.discord.ID+">")
	}

	msg := strings.NewReplacer(
		replacements...,
	).Replace(e.Last())

	if e.Command == "CTCP_ACTION" {
		msg = "_" + msg + "_"
	}

	msg = ircf.BlocksToMarkdown(ircf.Parse(msg))

	go func(e irc.Event) {
		i.bridge.discordMessagesChan <- IRCMessage{
			IRCChannel: e.Params[0],
			Username:   e.Source.Name,
			Message:    msg,
		}
	}(e)
}
