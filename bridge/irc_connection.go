package bridge

import (
	"fmt"
	"strings"
	"time"

	irc "git.tcp.direct/kayos/girc-atomic"
	log "github.com/sirupsen/logrus"

	"bridg/irc/varys"
)

// An ircConnection should only ever communicate with its manager
// Refer to `(m *ircManager) CreateConnection` to see how these are spawned
type ircConnection struct {
	discord DiscordUser
	nick    string

	quitMessage string

	messages      chan IRCMessage
	cooldownTimer *time.Timer

	manager *IRCManager

	// channel ID for their discord channel for PMs
	pmDiscordChannel string

	// Tell users this feature is in beta
	pmNoticed        bool
	pmNoticedSenders map[string]struct{}
}

func (i *ircConnection) GetNick() string {
	nick, err := i.manager.varys.GetNick(i.discord.ID)
	if err != nil {
		panic(err.Error())
	}
	return nick
}

func (i *ircConnection) Connected() bool {
	connected, err := i.manager.varys.Connected(i.discord.ID)
	if err != nil {
		panic(err.Error())
	}
	return connected
}

func (i *ircConnection) OnWelcome(c *irc.Client, e irc.Event) {
	go i.JoinChannels()

	go func(i *ircConnection) {
		for m := range i.messages {
			if len(m.Message) < 1 {
				continue
			}
			msg := m.Message
			if m.IsAction {
				msg = fmt.Sprintf("\001ACTION %s\001", msg)
			}
			i.Privmsg(m.IRCChannel, msg)
		}
	}(i)
}

func (i *ircConnection) JoinChannels() {
	i.SendRaw(i.manager.bridge.GetJoinCommand(i.manager.RequestChannels(i.discord.ID)))
}

func (i *ircConnection) UpdateDetails(discord DiscordUser) {
	if i.discord.Username != discord.Username {
		i.quitMessage = fmt.Sprintf("Changing real name from %s to %s", i.discord.Username, discord.Username)
		i.manager.CloseConnection(i)

		// After one second make the user reconnect.
		// This should be enough time for the nick tracker to update.
		time.AfterFunc(time.Second, func() {
			i.manager.HandleUser(discord)
		})
		return
	}

	// if their details haven't changed, don't do anything
	if (i.discord.Nick == discord.Nick) && (i.discord.Discriminator == discord.Discriminator) {
		return
	}

	i.discord = discord
	delete(i.manager.puppetNicks, i.nick)
	i.nick = i.manager.generateNickname(i.discord)
	i.manager.puppetNicks[i.nick] = i

	if err := i.manager.varys.Nick(i.discord.ID, i.nick); err != nil {
		panic(err.Error())
	}
}

func (i *ircConnection) introducePM(nick string) {
	d := i.manager.bridge.discord

	if i.pmDiscordChannel == "" {
		c, err := d.Session.UserChannelCreate(i.discord.ID)
		if err != nil {
			// todo: sentry
			log.Warnln("Could not create private message room", i.discord, err)
			return
		}
		i.pmDiscordChannel = c.ID
	}

	if !i.pmNoticed {
		i.pmNoticed = true
		_, err := d.Session.ChannelMessageSend(
			i.pmDiscordChannel,
			fmt.Sprintf("To reply type: `%s@%s, your message here`", nick, i.manager.bridge.Config.Discriminator))
		if err != nil {
			log.Warnln("Could not send pmNotice", i.discord, err)
			return
		}
	}

	nick = strings.ToLower(nick)
	if _, ok := i.pmNoticedSenders[nick]; !ok {
		i.pmNoticedSenders[nick] = struct{}{}
	}
}

func (i *ircConnection) OnPrivateMessage(c *irc.Client, e irc.Event) {
	// Ignored hostmasks
	if i.manager.isIgnoredHostmask(e.Source.String()) {
		return
	}

	// Alert private messages
	if string(e.Params[0][0]) != "#" {
		if e.Last() == "help" {
			i.Privmsg(e.Source.Name, "Commands: help, who")
		} else if e.Last() == "who" {
			i.Privmsg(e.Source.Name, fmt.Sprintf("I am: %s#%s with ID %s", i.discord.Nick, i.discord.Discriminator, i.discord.ID))
		}

		d := i.manager.bridge.discord

		i.introducePM(e.Source.Name)

		msg := fmt.Sprintf(
			"%s - %s@%s: %s", e.Source,
			e.Source.Name, i.manager.bridge.Config.Discriminator, e.Last())
		_, err := d.Session.ChannelMessageSend(i.pmDiscordChannel, msg)
		if err != nil {
			log.Warnln("Could not send PM", i.discord, err)
			return
		}
		return
	}

	// GTANet does not support deafness so the below logmsg has been disabled
	// log.Println("Non listener IRC connection received PRIVMSG from channel. Something went wrong.")
}

func (i *ircConnection) SendRaw(message string) {
	if err := i.manager.varys.SendRaw(i.discord.ID, varys.InterpolationParams{}, message); err != nil {
		panic(err.Error())
	}
}

func (i *ircConnection) SetAway(status string) {
	i.SendRaw(fmt.Sprintf("AWAY :%s", status))
}

func (i *ircConnection) Privmsg(target, message string) {
	i.SendRaw(fmt.Sprintf("PRIVMSG %s :%s\r\n", target, message))
}
