// Package varys is an abstraction that allows you to add or remove puppets,
// and receive a snapshot of state via an RPC-based interface.
//
// Why "varys"? Because it is the Master of Whisperers.
package varys

import (
	"crypto/tls"
	"fmt"
	"regexp"
	"strings"

	irc "git.tcp.direct/kayos/girc-atomic"
)

type Varys struct {
	connConfig SetupParams
	uidToConns map[string]*irc.Client
	Quit       chan bool
}

func NewVarys() *Varys {
	return &Varys{uidToConns: make(map[string]*irc.Client), Quit: make(chan bool)}
}

func (v *Varys) connCall(uid string, fn func(*irc.Client)) {
	if uid == "" {
		for _, conn := range v.uidToConns {
			fn(conn)
		}
		return
	}

	if conn, ok := v.uidToConns[uid]; ok {
		fn(conn)
	}
}

type Client interface {
	Setup(params SetupParams) error
	GetUIDToNicks() (map[string]string, error)
	Connect(params ConnectParams) error // Does not yet support netClient
	QuitIfConnected(uid string, quitMsg string) error
	Nick(uid string, nick string) error

	QuitAll()

	// SendRaw supports a blank uid to send to all connections.
	SendRaw(uid string, params InterpolationParams, messages ...string) error
	// GetNick gets the current connection's nick
	GetNick(uid string) (string, error)
	// Connected returns the status of the current connection
	Connected(uid string) (bool, error)
}

type SetupParams struct {
	UseTLS             bool // Whether we should use TLS
	InsecureSkipVerify bool // Controls tls.Config.InsecureSkipVerify, if using TLS

	Server         string
	Port           int
	ServerPassword string
	WebIRCPassword string
}

func (v *Varys) Setup(params SetupParams, _ *struct{}) error {
	v.connConfig = params
	v.Quit = make(chan bool)
	return nil
}

func (v *Varys) GetUIDToNicks(_ struct{}, result *map[string]string) error {
	conns := v.uidToConns
	m := make(map[string]string, len(conns))
	for uid, conn := range conns {
		m[uid] = conn.GetNick()
	}
	*result = m
	return nil
}

type ConnectParams struct {
	UID string

	Nick     string
	Username string
	RealName string

	WebIRCSuffix string

	// TODO(qaisjp): does not support net/rpc!!!!
	Callbacks map[string]func(c *irc.Client, e irc.Event)
}

func dontPanic(c *irc.Client, e *irc.HandlerError) {
	fmt.Println("PANIC!!", c.Config.Name, c.Config.User, c.Config.Nick, c.Config.Server, "PANIC!!")
	fmt.Println("PANIC!!", e, "PANIC!!")
}

func (v *Varys) Connect(params ConnectParams, _ *struct{}) error {
	defuckify, _ := regexp.Compile("[^a-zA-Z0-9]+")
	params.Username = defuckify.ReplaceAllString(params.Username, "o")
	conn := irc.Config{
		Server:      v.connConfig.Server,
		Port:        v.connConfig.Port,
		Nick:        params.Nick,
		User:        params.Username,
		Name:        params.RealName,
		Version:     "tcp.direct",
		AllowFlood:  false,
		RecoverFunc: dontPanic,

		SSL: false,
	}

	// TLS things, and the server password
	conn.ServerPass = v.connConfig.ServerPassword
	if v.connConfig.UseTLS {
		conn.SSL = true
	}

	if v.connConfig.InsecureSkipVerify {
		conn.TLSConfig = &tls.Config{
			InsecureSkipVerify: true,
		}
	}

	client := irc.New(conn)

	// On kick, rejoin the channel
	client.Handlers.Add("KICK", func(c *irc.Client, e irc.Event) {
		if e.Params[1] == client.GetNick() {
			c.Cmd.Join(e.Params[0])
		}
	})

	for eventcode, callback := range params.Callbacks {
		client.Handlers.Add(eventcode, callback)
	}

	start := make(chan bool)

	go func() {
		for {
			select {
			case <-v.Quit:
				for _, c := range v.uidToConns {
					if c == nil {
						continue
					}
					if c.IsConnected() {
						c.Quit("shutting down")
					}
				}
				return
			case <-start:
				err := client.Connect()
				if err != nil {
					fmt.Printf("error opening irc connection: %s", err.Error())
				}
			default:
				//
			}
		}
	}()
	start <- true
	v.uidToConns[params.UID] = client
	return nil
}

type QuitParams struct {
	UID         string
	QuitMessage string
}

func (v *Varys) QuitIfConnected(params QuitParams, _ *struct{}) error {
	if conn, ok := v.uidToConns[params.UID]; ok {
		if conn.IsConnected() {
			conn.Quit(params.QuitMessage)
		}
	}
	delete(v.uidToConns, params.UID)
	return nil
}

type InterpolationParams struct {
	Nick bool
}
type SendRawParams struct {
	UID      string
	Messages []string

	Interpolation InterpolationParams
}

func (v *Varys) SendRaw(params SendRawParams, _ *struct{}) error {
	v.connCall(params.UID, func(c *irc.Client) {
		for _, msg := range params.Messages {
			if params.Interpolation.Nick {
				msg = strings.ReplaceAll(msg, "${NICK}", c.GetNick())
			}
			c.Cmd.SendRaw(msg)
		}
	})
	return nil
}

func (v *Varys) GetNick(uid string, result *string) error {
	if conn, ok := v.uidToConns[uid]; ok {
		*result = conn.GetNick()
	}
	return nil
}

func (v *Varys) Connected(uid string, result *bool) error {
	if conn, ok := v.uidToConns[uid]; ok {
		*result = conn.IsConnected()
	}

	return nil
}

type NickParams struct {
	UID  string
	Nick string
}

func (v *Varys) Nick(params NickParams, _ *struct{}) error {
	if conn, ok := v.uidToConns[params.UID]; ok {
		conn.Cmd.Nick(params.Nick)
	}
	return nil
}
