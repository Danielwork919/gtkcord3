package channel

import (
	"sort"
	"strings"
	"sync"

	"github.com/diamondburned/arikawa/discord"
	"github.com/diamondburned/arikawa/gateway"
	"github.com/diamondburned/gtkcord3/gtkcord/gtkutils"
	"github.com/diamondburned/gtkcord3/gtkcord/ningen"
	"github.com/diamondburned/gtkcord3/gtkcord/semaphore"
	"github.com/diamondburned/gtkcord3/log"
	"github.com/gotk3/gotk3/gtk"
)

type PrivateChannels struct {
	gtkutils.ExtendedWidget

	List   *gtk.ListBox
	Scroll *gtk.ScrolledWindow

	Search *gtk.Entry
	search string

	// Channels map[discord.Snowflake]*PrivateChannel
	Channels map[string]*PrivateChannel

	busy  sync.RWMutex
	state *ningen.State

	OnSelect func(pm *PrivateChannel)
}

// thread-safe
func NewPrivateChannels(s *ningen.State) (pcs *PrivateChannels) {
	semaphore.IdleMust(func() {
		l, _ := gtk.ListBoxNew()
		l.Show()
		gtkutils.InjectCSSUnsafe(l, "dmchannels", "")

		cs, _ := gtk.ScrolledWindowNew(nil, nil)
		cs.Show()
		cs.SetSizeRequest(ChannelsWidth, -1)
		cs.SetVExpand(true)
		cs.Add(l)

		e, _ := gtk.EntryNew()
		e.Show()
		e.SetPlaceholderText("Find conversation...")

		b, _ := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
		b.Show()
		b.Add(e)
		b.Add(cs)

		pcs = &PrivateChannels{
			ExtendedWidget: b,

			List:   l,
			Scroll: cs,
			Search: e,

			state: s,
		}

		e.Connect("changed", func() {
			t, err := e.GetText()
			if err != nil {
				log.Errorln("Failed to get text from dmchannels entry:", err)
				return
			}

			pcs.search = strings.ToLower(t)
			pcs.List.InvalidateFilter()
		})

		l.SetFilterFunc(pcs.filter, 0)
		l.Connect("selected-rows-changed", func(l *gtk.ListBox) {
			var r = l.GetSelectedRow()

			if len(pcs.Channels) == 0 || pcs.OnSelect == nil || r == nil {
				return
			}

			rw, ok := pcs.Channels[_ChIDFromRow(r)]
			if !ok {
				log.Errorln("Failed to find channel")
				return
			}

			go pcs.OnSelect(rw)
		})
	})

	s.AddReadChange(pcs.TraverseReadState)
	return
}

func (pcs *PrivateChannels) Cleanup() {
	pcs.busy.Lock()
	defer pcs.busy.Unlock()

	if pcs.Channels != nil {
		semaphore.IdleMust(func() {
			for _, ch := range pcs.Channels {
				pcs.List.Remove(ch)
			}
		})

		pcs.Channels = nil
	}
}

// thread-safe
func (pcs *PrivateChannels) LoadChannels(channels []discord.Channel) {
	pcs.busy.Lock()
	defer pcs.busy.Unlock()

	pcs.Channels = make(map[string]*PrivateChannel, len(channels))

	sort.Slice(channels, func(i, j int) bool {
		return channels[i].LastMessageID > channels[j].LastMessageID
	})

	semaphore.IdleMust(func() {
		for _, channel := range channels {
			w := newPrivateChannel(channel)

			if channel.Type == discord.DirectMessage && len(channel.DMRecipients) == 1 {
				user := channel.DMRecipients[0]
				w.updateAvatar(user.AvatarURL())

				if p, _ := pcs.state.Presence(0, user.ID); p != nil {
					var game = p.Game
					if game == nil && len(p.Activities) > 0 {
						game = &p.Activities[0]
					}

					w.updateStatus(p.Status)
					w.updateActivity(game)
				}

			} else if channel.Icon != "" {
				w.updateAvatar(channel.IconURL())
			}

			pcs.Channels[channel.ID.String()] = w
			pcs.List.Insert(w, -1)
		}
	})
}

func (pcs *PrivateChannels) Selected() *PrivateChannel {
	pcs.busy.RLock()
	defer pcs.busy.RUnlock()

	if len(pcs.Channels) == 0 {
		return nil
	}

	r := pcs.List.GetSelectedRow()
	if r == nil {
		r = pcs.List.GetRowAtIndex(0)
		pcs.List.SelectRow(r)
	}

	rw, ok := pcs.Channels[_ChIDFromRow(r)]
	if !ok || rw == nil {
		log.Errorln("Failed to find channel row")
	}
	return rw
}

func (pcs *PrivateChannels) filter(r *gtk.ListBoxRow, _ ...interface{}) bool {
	if pcs.search == "" {
		return true
	}

	pc, ok := pcs.Channels[_ChIDFromRow(r)]
	if !ok {
		log.Errorln("Failed to get channel for filter")
		return false
	}

	return strings.Contains(strings.ToLower(pc.Name), pcs.search)
}

func (pcs *PrivateChannels) TraverseReadState(_ *ningen.State, rs *gateway.ReadState, unread bool) {
	// Read lock is used, as the size of the slice isn't directly modified.
	pcs.busy.RLock()
	defer pcs.busy.RUnlock()

	pc, ok := pcs.Channels[rs.ChannelID.String()]
	if !ok {
		return
	}

	// Prepend/move to top.
	semaphore.IdleMust(func() {
		pcs.List.Remove(pc)
		pcs.List.Prepend(pc)
	})

	pc.setUnread(unread)
}

// func (pcs *PrivateChannels) updatePresence(p discord.Presence) {
// 	for _, ch := range pcs.Channels {
// 		if ch.Recp == p.User.ID && !ch.Group {
// 			ch.UpdateStatus(p.Status)
// 			ch.UpdateActivity(p.Game)
// 			break
// 		}
// 	}
// }

// func (pcs *PrivateChannels) setUnread(unread bool) {
// 	if !unread {
// 		for _, ch := range pcs.Channels {
// 			if ch.stateClass == "pinged" {
// 				unread = true
// 				break
// 			}
// 		}
// 	}

// 	if unread {
// 		pcs.setButtonClass("pinged")
// 	} else {
// 		pcs.setButtonClass("")
// 	}
// }
