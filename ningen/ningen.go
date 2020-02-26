package ningen

import (
	"sync"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/diamondburned/arikawa/api"
	"github.com/diamondburned/arikawa/discord"
	"github.com/diamondburned/arikawa/gateway"
	"github.com/diamondburned/arikawa/state"
	"github.com/diamondburned/gtkcord3/log"
)

type State struct {
	*state.State

	mutedMutex    sync.Mutex
	MutedGuilds   map[discord.Snowflake]*Mute
	MutedChannels map[discord.Snowflake]*Mute

	readMutex    sync.Mutex
	lastAck      api.Ack
	lastAckTimes map[discord.Snowflake]time.Time // channelID
	LastRead     map[discord.Snowflake]*gateway.ReadState

	OnReadUpdate     func(*gateway.ReadState)
	OnGuildPosChange func(*gateway.UserSettings)
}

type Mute struct {
	// if true, then muted
	All           bool
	Notifications int // some sort of constant?

	// guild only
	Everyone bool // @everyone
}

func Ningen(s *state.State) (*State, error) {
	state := &State{
		State:         s,
		MutedGuilds:   map[discord.Snowflake]*Mute{},
		MutedChannels: map[discord.Snowflake]*Mute{},
		LastRead:      map[discord.Snowflake]*gateway.ReadState{},
		lastAckTimes:  map[discord.Snowflake]time.Time{},
		OnReadUpdate: func(r *gateway.ReadState) {
			log.Println("Read state update in channel", r.ChannelID, "message ID", r.LastMessageID)
		},
		OnGuildPosChange: func(*gateway.UserSettings) {},
	}

	s.AddHandler(func(a *gateway.MessageAckEvent) {
		state.hookIncomingMessage(a.ChannelID, a.MessageID)
	})

	s.AddHandler(func(c *gateway.MessageCreateEvent) {
		state.hookIncomingMessage(c.ChannelID, c.ID)
	})

	s.AddHandler(func(r *gateway.ReadyEvent) {
		state.UpdateReady(*r)
	})

	s.AddHandler(func(r *gateway.UserSettingsUpdateEvent) {
		state.OnGuildPosChange((*gateway.UserSettings)(r))
	})

	s.AddHandler(func(u *gateway.UserGuildSettingsUpdateEvent) {
		state.updateMuteState([]gateway.UserGuildSettings{
			gateway.UserGuildSettings(*u),
		})
	})

	return state, nil
}

func (s *State) UpdateReady(r gateway.ReadyEvent) {
	s.updateMuteState(r.UserGuildSettings)
	s.updateReadState(r.ReadState)
}

func (s *State) updateMuteState(ugses []gateway.UserGuildSettings) {
	// TODO: This function doesn't have any callbacks to indicate this update.
	// There should be a better way to know what to call on. This is required
	// for things like updated muting states, mainly UI changes.

	log.Infoln("Received mute states", spew.Sdump(ugses))

	s.mutedMutex.Lock()
	defer s.mutedMutex.Unlock()

	for _, ugs := range ugses {
		mg, ok := s.MutedGuilds[ugs.GuildID]
		if !ok {
			mg = &Mute{}
			s.MutedGuilds[ugs.GuildID] = mg
		}

		mg.All = ugs.Muted
		mg.Everyone = ugs.SupressEveryone
		mg.Notifications = ugs.MessageNotifications

		for _, ch := range ugs.ChannelOverrides {
			mc, ok := s.MutedChannels[ch.ChannelID]
			if !ok {
				mc = &Mute{}
				s.MutedChannels[ch.ChannelID] = mc
			}

			mc.All = ch.Muted
			mc.Notifications = ch.MessageNotifications
		}
	}
}

func (s *State) updateReadState(rs []gateway.ReadState) {
	s.readMutex.Lock()
	defer s.readMutex.Unlock()

	old := len(s.LastRead)

	for _, read := range rs {
		s.LastRead[read.ChannelID] = &gateway.ReadState{
			ChannelID:     read.ChannelID,
			LastMessageID: read.LastMessageID,
			MentionCount:  read.MentionCount,
		}
	}

	// If this is our first time, we'll try and brute our way through:
	if old > 0 {
		for _, rs := range s.LastRead {
			s.OnReadUpdate(rs)
		}
	}
}

// returns *ReadState if updated, marks the message as unread.
func (s *State) hookIncomingMessage(channel, message discord.Snowflake) bool {
	s.readMutex.Lock()
	defer s.readMutex.Unlock()

	st, ok := s.LastRead[channel]
	if !ok {
		st = &gateway.ReadState{
			ChannelID: channel,
		}
		s.LastRead[channel] = st

	} else if st.LastMessageID == message {
		return false
	}

	st.LastMessageID = message

	// Only call the Read update handler when the channel or guild is not muted.
	if !s.ChannelMuted(channel) {
		s.OnReadUpdate(st)
	}
	return true
}

func (s *State) FindLastRead(channelID discord.Snowflake) *gateway.ReadState {
	if s.ChannelMuted(channelID) {
		return nil
	}

	s.readMutex.Lock()
	defer s.readMutex.Unlock()

	if s, ok := s.LastRead[channelID]; ok {
		return s
	}

	return nil
}

func (s *State) MarkRead(channelID, messageID discord.Snowflake) {
	// Update ReadState as well as the callback.
	if !s.hookIncomingMessage(channelID, messageID) {
		return
	}

	// TODO: make this a select default loop, since this just cancels the latest
	// message ID, but does not mark them afterwards.

	s.readMutex.Lock()
	now := time.Now()

	t, ok := s.lastAckTimes[channelID]
	if ok {
		// If we've ack'd in the past 10 seconds:
		if t.Add(10 * time.Second).After(now) {
			s.readMutex.Unlock()
			return
		}
	}
	s.lastAckTimes[channelID] = now

	s.readMutex.Unlock()

	// Send over Ack.
	if err := s.Ack(channelID, messageID, &s.lastAck); err != nil {
		log.Errorln("Failed to ack message:", err)
	}
}

func (s *State) ChannelMuted(channelID discord.Snowflake) bool {
	s.mutedMutex.Lock()
	if _, ok := s.MutedChannels[channelID]; ok {
		s.mutedMutex.Unlock()
		return true
	}
	s.mutedMutex.Unlock()

	ch, err := s.Store.Channel(channelID)
	if err != nil {
		log.Errorln("Failed to get channel in FindLastRead:", err)
	}

	if ch.GuildID.Valid() {
		return s.GuildMuted(ch.GuildID)
	}
	return false
}

func (s *State) GuildMuted(guildID discord.Snowflake) bool {
	s.mutedMutex.Lock()
	defer s.mutedMutex.Unlock()

	_, ok := s.MutedGuilds[guildID]
	return ok
}
