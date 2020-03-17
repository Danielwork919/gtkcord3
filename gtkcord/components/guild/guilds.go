package guild

import (
	"sort"

	"github.com/diamondburned/arikawa/discord"
	"github.com/diamondburned/arikawa/gateway"
	"github.com/diamondburned/gtkcord3/gtkcord/gtkutils"
	"github.com/diamondburned/gtkcord3/gtkcord/ningen"
	"github.com/diamondburned/gtkcord3/gtkcord/semaphore"
	"github.com/gotk3/gotk3/gtk"
	"github.com/pkg/errors"
)

type Guilds struct {
	gtkutils.ExtendedWidget

	ListBox  *gtk.ListBox
	Avatar   *Avatar
	DMButton *DMButton

	Guilds   []gtkutils.ExtendedWidget
	Current  *Guild
	OnSelect func(g *Guild)
}

func NewGuilds(s *ningen.State) (*Guilds, error) {
	if len(s.Ready.Settings.GuildFolders) > 0 {
		return NewGuildsFromFolders(s, s.Ready.Settings.GuildFolders)
	} else {
		return NewGuildsLegacy(s, s.Ready.Settings.GuildPositions)
	}
}

func NewGuildsFromFolders(s *ningen.State, folders []gateway.GuildFolder) (*Guilds, error) {
	var rows = make([]gtkutils.ExtendedWidget, 0, len(folders))
	var g = &Guilds{}

	for i := 0; i < len(folders); i++ {
		f := folders[i]

		if len(f.GuildIDs) == 1 {
			r, err := newGuildRow(s, f.GuildIDs[0], nil, nil)
			if err != nil {
				return nil, errors.Wrap(err, "Failed to load guild "+f.GuildIDs[0].String())
			}

			rows = append(rows, r)

		} else {
			e, err := newGuildFolder(s, f, g.onFolderSelect)
			if err != nil {
				return nil, errors.Wrap(err, "Failed to create a new folder "+f.Name)
			}

			rows = append(rows, e)
		}
	}

	g.Guilds = rows
	initGuilds(g, s)
	return g, nil
}

func NewGuildsLegacy(s *ningen.State, positions []discord.Snowflake) (*Guilds, error) {
	guilds, err := s.Guilds()
	if err != nil {
		return nil, errors.Wrap(err, "Failed to get guilds")
	}

	var rows = make([]gtkutils.ExtendedWidget, 0, len(guilds))

	sort.Slice(guilds, func(a, b int) bool {
		var found = false
		for _, guild := range positions {
			if found && guild == guilds[b].ID {
				return true
			}
			if !found && guild == guilds[a].ID {
				found = true
			}
		}

		return false
	})

	for _, g := range guilds {
		r, err := newGuildRow(s, g.ID, &g, nil)
		if err != nil {
			return nil, errors.Wrap(err, "Failed to load guild "+g.Name)
		}

		rows = append(rows, r)
	}

	g := &Guilds{
		Guilds: rows,
	}
	initGuilds(g, s)
	return g, nil
}

func initGuilds(g *Guilds, s *ningen.State) {
	dm := NewPMButton()

	semaphore.IdleMust(func() {
		gw, _ := gtk.ScrolledWindowNew(nil, nil)
		gw.Show()
		gw.SetPolicy(gtk.POLICY_NEVER, gtk.POLICY_AUTOMATIC)
		g.ExtendedWidget = gw

		l, _ := gtk.ListBoxNew()
		l.Show()
		l.SetActivateOnSingleClick(true)
		gtkutils.InjectCSSUnsafe(l, "guilds", "")

		gw.Add(l)
		g.ListBox = l

		// Create the user button and add it first:
		a := NewAvatar(s)
		g.Avatar = a
		l.Insert(a, -1)

		// Add the button to the second of the list:
		g.DMButton = dm
		l.Insert(dm, -1)

		// Add the rest:
		for _, g := range g.Guilds {
			l.Insert(g, -1)
			g.ShowAll()
		}

		l.Connect("row-activated", func(l *gtk.ListBox, r *gtk.ListBoxRow) {
			var index = r.GetIndex()

			switch {
			case index < 1:
				a.OnClick()
				return
			case index == 1:
				go g.DMButton.OnClick()
				return
			default:
				index -= 2
			}

			var row = g.Guilds[index]

			// Unselect all guild folders except the current one:
			for i, r := range g.Guilds {
				if i == index {
					continue
				}
				if f, ok := r.(*GuildFolder); ok {
					f.List.SelectRow(nil)
				}
			}

			// load the guild, then subscribe to typing events
			d, ok := row.(*Guild)
			if ok {
				g.onSelect(d)
			}
		})
	})

	go func() {
		// Update the avatar's status and avatar:
		g.Avatar.CheckUpdate()

		g.Find(func(g *Guild) bool {
			g.UpdateImage()
			return false
		})
	}()

	s.AddReadChange(g.TraverseReadState)
}

func (guilds *Guilds) onFolderSelect(g *Guild) {
	guilds.ListBox.SelectRow(nil)
	guilds.onSelect(g)
}

func (guilds *Guilds) onSelect(g *Guild) {
	if guilds.OnSelect == nil {
		return
	}

	guilds.Current = g
	go guilds.OnSelect(g)
}

func (guilds *Guilds) FindByID(guildID discord.Snowflake) (*Guild, *GuildFolder) {
	return guilds.Find(func(g *Guild) bool {
		return g.ID == guildID
	})
}

func (guilds *Guilds) Find(fn func(*Guild) bool) (*Guild, *GuildFolder) {
	for _, v := range guilds.Guilds {
		switch v := v.(type) {
		case *Guild:
			if fn(v) {
				return v, nil
			}
		case *GuildFolder:
			for _, guild := range v.Guilds {
				if fn(guild) {
					return guild, v
				}
			}
		}
	}

	return nil, nil
}

func (guilds *Guilds) TraverseReadState(s *ningen.State, rs *gateway.ReadState, unread bool) {
	ch, err := s.Store.Channel(rs.ChannelID)
	if err != nil {
		// log.Errorln("Failed to find channel:", err)
		return
	}
	if !ch.GuildID.Valid() {
		// DM:
		guilds.DMButton.setUnread(unread)
		return
	}

	guild, _ := guilds.FindByID(ch.GuildID)
	if guild == nil {
		return
	}

	pinged := rs.MentionCount > 0

	guild.busy.Lock()

	if !unread {
		delete(guild.unreadChs, rs.ChannelID)
	} else {
		guild.unreadChs[rs.ChannelID] = pinged
	}

	if !unread || !pinged {
		for _, chPinged := range guild.unreadChs {
			unread = true
			if pinged {
				break
			}
			if !pinged && chPinged {
				pinged = true
			}
		}
	}

	guild.busy.Unlock()

	guild.setUnread(unread, pinged)
}
