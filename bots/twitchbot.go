package bots

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"

	twitch "github.com/gempir/go-twitch-irc"
	"github.com/pajlada/pajbot2/common"
	"github.com/pajlada/pajbot2/pkg"
	"github.com/pajlada/pajbot2/pkg/channels"
	"github.com/pajlada/pajbot2/pkg/users"
	"github.com/pajlada/pajbot2/pkg/utils"
	"github.com/pajlada/pajbot2/redismanager"
)

type ModeState int

const (
	ModeUnset = iota
	ModeEnabled
	ModeDisabled
)

type botFlags struct {
	PermaSubMode bool
}

// TwitchBot is a wrapper around go-twitch-irc's twitch.Client with a few extra features
type TwitchBot struct {
	*twitch.Client

	Name    string
	handler Handler

	QuitChannel chan string

	Flags botFlags

	Redis *redismanager.RedisManager

	Modules []pkg.Module

	// TODO: Store one point server per channel the bot is in. share between bots
	pointServer *PointServer
}

type emoteReader struct {
	index int

	emotes *[]*common.Emote

	started bool
}

func newEmoteHolder(emotes *[]*common.Emote) *emoteReader {
	return &emoteReader{
		index:  0,
		emotes: emotes,
	}
}

func (h *emoteReader) Next() bool {
	if !h.started {
		h.started = true

		if len(*h.emotes) == 0 {
			return false
		}

		return true
	}

	h.index++

	if h.index >= len(*h.emotes) {
		return false
	}

	return true
}

func (h *emoteReader) Get() pkg.Emote {
	return (*h.emotes)[h.index]
}

// TwitchMessage is a wrapper for twitch.Message with some extra stuff
type TwitchMessage struct {
	twitch.Message

	twitchEmotes      []*common.Emote
	twitchEmoteReader *emoteReader

	bttvEmotes      []*common.Emote
	bttvEmoteReader *emoteReader
	// TODO: BTTV Emotes

	// TODO: FFZ Emotes

	// TODO: Emojis
}

func NewTwitchMessage(message twitch.Message) *TwitchMessage {
	msg := &TwitchMessage{
		Message: message,
	}
	msg.twitchEmoteReader = newEmoteHolder(&msg.twitchEmotes)
	msg.bttvEmoteReader = newEmoteHolder(&msg.bttvEmotes)

	return msg
}

func (m TwitchMessage) GetText() string {
	return m.Text
}

func (m TwitchMessage) GetTwitchReader() pkg.EmoteReader {
	return m.twitchEmoteReader
}

func (m TwitchMessage) GetBTTVReader() pkg.EmoteReader {
	return m.bttvEmoteReader
}

func (m *TwitchMessage) AddBTTVEmote(emote pkg.Emote) {
	m.bttvEmotes = append(m.bttvEmotes, emote.(*common.Emote))
}

func (b *TwitchBot) RegisterModules() error {
	for _, m := range b.Modules {
		err := m.Register()
		if err != nil {
			return err
		}
	}

	return nil
}

// Reply will reply to the message in the same way it received the message
// If the message was received in a twitch channel, reply in that twitch channel.
// IF the message was received in a twitch whisper, reply using twitch whispers.
func (b *TwitchBot) Reply(channel pkg.Channel, user pkg.User, message string) {
	if channel == nil {
		b.Whisper(user, message)
	} else {
		b.Say(channel, message)
	}
}

func (b *TwitchBot) Say(channel pkg.Channel, message string) {
	b.Client.Say(channel.GetChannel(), message)
}

func (b *TwitchBot) Mention(channel pkg.Channel, user pkg.User, message string) {
	b.Client.Say(channel.GetChannel(), "@"+user.GetName()+", "+message)
}

func (b *TwitchBot) Whisper(user pkg.User, message string) {
	b.Client.Whisper(user.GetName(), message)
}

func (b *TwitchBot) Timeout(channel pkg.Channel, user pkg.User, duration int, reason string) {
	// Empty string in UserType means a normal user
	if !user.IsModerator() {
		b.Say(channel, fmt.Sprintf(".timeout %s %d %s", user.GetName(), duration, reason))
	}
}

// SetHandler sets the handler to message at the bottom of the list
func (b *TwitchBot) SetHandler(handler Handler) {
	b.handler = handler
}

func (b *TwitchBot) HandleWhisper(user twitch.User, rawMessage twitch.Message) {
	message := NewTwitchMessage(rawMessage)

	twitchUser := &users.TwitchUser{
		User: user,

		ID: message.Tags["user-id"],
	}

	action := &pkg.TwitchAction{
		Sender: b,
		User:   twitchUser,
	}

	b.handler.HandleMessage(b, nil, twitchUser, message, action)

	if pkg.VerboseMessages {
		log.Printf("%s - @%s(%s): %s", b.Name, twitchUser.DisplayName, twitchUser.Username, message.Text)
	}
}

func (b *TwitchBot) HandleMessage(channelName string, user twitch.User, rawMessage twitch.Message) {
	message := NewTwitchMessage(rawMessage)

	twitchUser := &users.TwitchUser{
		User: user,

		ID: message.Tags["user-id"],
	}

	channel := &channels.TwitchChannel{
		Channel: channelName,
	}

	action := &pkg.TwitchAction{
		Sender:  b,
		Channel: channel,
		User:    twitchUser,
	}

	for _, emote := range rawMessage.Emotes {
		parsedEmote := &common.Emote{
			Name:  emote.Name,
			ID:    emote.ID,
			Count: emote.Count,
			Type:  "twitch",
		}
		message.twitchEmotes = append(message.twitchEmotes, parsedEmote)
	}

	b.handler.HandleMessage(b, channel, twitchUser, message, action)

	if pkg.VerboseMessages {
		log.Printf("%s - #%s: %s(%s): %s", b.Name, channel, twitchUser.DisplayName, twitchUser.Username, message.Text)
	}
}

func (b *TwitchBot) HandleRoomstateMessage(channelName string, user twitch.User, rawMessage twitch.Message) {
	subMode := ModeUnset

	channel := &channels.TwitchChannel{
		Channel: channelName,
	}

	if readSubMode, ok := rawMessage.Tags["subs-only"]; ok {
		if readSubMode == "1" {
			subMode = ModeEnabled
		} else {
			subMode = ModeDisabled
		}
	}

	if subMode != ModeUnset {
		if subMode == ModeEnabled {
			log.Printf("Submode enabled")
		} else {
			log.Printf("Submode disabled")

			if b.Flags.PermaSubMode {
				b.Say(channel, "Perma sub mode is enabled. A mod can type !suboff to disable perma sub mode")
				b.Say(channel, ".subscribers")
			}
		}
	}

	log.Printf("%s - #%s: %#v: %#v", b.Name, channel, user, rawMessage)
}

// Quit quits the entire application
func (b *TwitchBot) Quit(message string) {
	b.QuitChannel <- message
}

type PointServer struct {
	conn net.Conn
}

func (p *PointServer) Send(command uint8, body []byte) {
	bodyLength := make([]byte, 4)
	binary.BigEndian.PutUint32(bodyLength, uint32(len(body)))

	// Write header (Command + Body length)
	p.conn.Write(append([]byte{command}, bodyLength...))

	// Write body
	p.conn.Write(body)
}

func (p *PointServer) Read(size int) []byte {
	reader := bufio.NewReader(p.conn)

	response := make([]byte, size)

	io.ReadFull(reader, response)

	return response
}

func newPointServer(host string) (*PointServer, error) {
	conn, err := net.Dial("tcp", host)
	if err != nil {
		return nil, err
	}

	return &PointServer{
		conn: conn,
	}, nil
}

func (b *TwitchBot) ConnectToPointServer() (err error) {
	// TODO: read from config file
	b.pointServer, err = newPointServer("localhost:54321")
	if err != nil {
		return
	}

	// TODO: connect once per channel
	b.pointServer.Send(CommandConnect, []byte("pajlada"))

	return
}

const (
	CommandConnect    = 0x01
	CommandGetPoints  = 0x02
	CommandEditPoints = 0x03
)

func (b *TwitchBot) GetPoints(channel pkg.Channel, user pkg.User) uint64 {
	bodyPayload := []byte(user.GetID())

	b.pointServer.Send(CommandGetPoints, bodyPayload)
	response := b.pointServer.Read(8)

	return binary.BigEndian.Uint64(response)
}

func (b *TwitchBot) EditPoints(channel pkg.Channel, user pkg.User, points int32) uint64 {
	var bodyPayload []byte
	bodyPayload = append(bodyPayload, utils.Int32ToBytes(points)...)
	bodyPayload = append(bodyPayload, []byte(user.GetID())...)

	b.pointServer.Send(CommandEditPoints, bodyPayload)
	response := b.pointServer.Read(8)

	return binary.BigEndian.Uint64(response)
}
