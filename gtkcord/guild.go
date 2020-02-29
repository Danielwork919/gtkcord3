package gtkcord

import (
	"html"
	"sort"
	"sync"

	"github.com/diamondburned/arikawa/discord"
	"github.com/diamondburned/arikawa/gateway"
	"github.com/diamondburned/gtkcord3/gtkcord/cache"
	"github.com/diamondburned/gtkcord3/gtkcord/gtkutils"
	"github.com/diamondburned/gtkcord3/log"
	"github.com/gotk3/gotk3/gtk"
	"github.com/pkg/errors"
)

const (
	FolderSize  = 42
	IconSize    = 52
	IconPadding = 8
)

type Guilds struct {
	*gtk.ListBox

	Friends *gtk.TreeIter // TODO
	Guilds  []*Guild
}

type Guild struct {
	Guilds *Guilds
	Parent *Guild

	gtkutils.ExtendedWidget
	Row   *gtk.ListBoxRow
	Style *gtk.StyleContext

	Folder *GuildFolder

	Image *gtk.Image
	IURL  string

	BannerURL string

	ID   discord.Snowflake
	Name string

	// nil if Folder
	Channels *Channels
	current  *Channel

	requestingMembers  map[discord.Snowflake]struct{}
	requestingMemMutex sync.Mutex

	unread bool
}

func newGuildsFromFolders() ([]*Guild, error) {
	var folders = App.State.Ready.Settings.GuildFolders
	var rows = make([]*Guild, 0, len(folders))

	for i := 0; i < len(folders); i++ {
		f := folders[i]

		if len(f.GuildIDs) == 1 && f.Name == "" {
			r, err := newGuildRow(f.GuildIDs[0])
			if err != nil {
				return nil, errors.Wrap(err, "Failed to load guild "+f.GuildIDs[0].String())
			}

			rows = append(rows, r)

		} else {
			e, err := newGuildFolder(f)
			if err != nil {
				return nil, errors.Wrap(err, "Failed to create a new folder "+f.Name)
			}

			rows = append(rows, e)
		}
	}

	return rows, nil
}

func newGuildsLegacy() ([]*Guild, error) {
	s := App.State

	gs, err := s.Guilds()
	if err != nil {
		return nil, errors.Wrap(err, "Failed to get guilds")
	}

	var pos = s.Ready.Settings.GuildPositions
	var rows = make([]*Guild, 0, len(gs))

	sort.Slice(gs, func(a, b int) bool {
		var found = false
		for _, guild := range pos {
			if found && guild == gs[b].ID {
				return true
			}
			if !found && guild == gs[a].ID {
				found = true
			}
		}

		return false
	})

	for _, g := range gs {
		r, err := newGuildRow(g.ID)
		if err != nil {
			return nil, errors.Wrap(err, "Failed to load guild "+g.Name)
		}

		rows = append(rows, r)
	}

	return rows, nil
}

func newGuilds() (*Guilds, error) {
	var rows []*Guild
	var err error

	if len(App.State.Ready.Settings.GuildPositions) > 0 {
		rows, err = newGuildsFromFolders()
	} else {
		rows, err = newGuildsLegacy()
	}

	if err != nil {
		return nil, errors.Wrap(err, "Failed to get guilds list")
	}

	l := must(gtk.ListBoxNew).(*gtk.ListBox)
	must(l.SetActivateOnSingleClick, true)
	gtkutils.InjectCSS(l, "guilds", "")

	g := &Guilds{
		ListBox: l,
		Guilds:  rows,
	}

	for i := 0; i < len(rows); i++ {
		must(l.Add, rows[i])
		must(rows[i].ShowAll)
	}

	must(l.Connect, "row-activated", func(l *gtk.ListBox, r *gtk.ListBoxRow) {
		index := r.GetIndex()
		row := rows[index]

		// Collapse all revealers:
		for i, r := range rows {
			if i == index {
				continue
			}
			if r.Folder != nil {
				r.Folder.Revealer.SetRevealChild(false)
			}
		}

		if row.Folder != nil {
			index := row.Folder.List.GetSelectedRow().GetIndex()
			if index < 0 {
				index = 0
				row.Folder.List.SelectRow(row.Folder.Guilds[0].Row)
			}

			row = row.Folder.Guilds[index]
		}

		go func() {
			App.loadGuild(row)
		}()
	})

	g.find(func(g *Guild) bool {
		g.UpdateImage()
		return false
	})

	return g, nil
}

func (guilds *Guilds) findByID(guildID discord.Snowflake) (*Guild, *GuildFolder) {
	return guilds.find(func(g *Guild) bool {
		return g.ID == guildID
	})
}

func (guilds *Guilds) find(fn func(*Guild) bool) (*Guild, *GuildFolder) {
	for _, guild := range guilds.Guilds {
		if guild.Folder == nil && fn(guild) {
			return guild, nil
		}

		if guild.Folder != nil {
			folder := guild.Folder

			for _, guild := range folder.Guilds {
				if fn(guild) {
					return guild, folder
				}
			}
		}
	}

	return nil, nil
}

func newGuildRow(guildID discord.Snowflake) (*Guild, error) {
	defer log.Benchmark("newGuildRow")()

	g, fetcherr := App.State.Guild(guildID)
	if fetcherr != nil {
		log.Errorln("Failed to get guild ID " + guildID.String() + ", using a placeholder...")
		g = &discord.Guild{
			ID:   guildID,
			Name: "Unavailable",
		}
	}

	r := must(gtk.ListBoxRowNew).(*gtk.ListBoxRow)
	// Set paddings:
	must(r.SetSizeRequest, IconSize+IconPadding*2, IconSize+IconPadding*2)
	must(r.SetHAlign, gtk.ALIGN_FILL)
	must(r.SetVAlign, gtk.ALIGN_CENTER)
	must(r.SetTooltipMarkup, bold(g.Name))
	must(r.SetActivatable, true)

	style := must(r.GetStyleContext).(*gtk.StyleContext)
	must(style.AddClass, "guild")

	i := must(gtk.ImageNewFromIconName, "user-available", gtk.ICON_SIZE_DIALOG).(*gtk.Image)
	must(r.Add, i)

	guild := &Guild{
		ExtendedWidget: r,

		Row:       r,
		Style:     style,
		ID:        guildID,
		Name:      g.Name,
		IURL:      g.IconURL(),
		Image:     i,
		BannerURL: g.BannerURL(),
	}

	// Check if the guild is unavailable:
	if fetcherr != nil {
		must(guild.SetUnavailable, true)
		return guild, nil
	}

	// Prefetch unread state:
	go func() {
		if err := guild.prefetchChannels(); err != nil {
			log.Errorln("Failed to prefetch guild", guild.Name)
			return
		}

		for _, ch := range guild.Channels.Channels {
			if rs := App.State.FindLastRead(ch.ID); rs != nil {
				if ch.LastMsg != rs.LastMessageID {
					guild.setUnread(true)
					break
				}
			}
		}
	}()

	return guild, nil
}

func (g *Guild) SetUnavailable(unavailable bool) {
	g.Row.SetSensitive(!unavailable)
}

func (g *Guild) Current() *Channel {
	if g.current != nil {
		return g.current
	}

	index := -1
	current := must(g.Channels.ChList.GetSelectedRow).(*gtk.ListBoxRow)

	if current == nil {
		index = g.Channels.First()
	} else {
		index = must(current.GetIndex).(int)
	}

	if index < 0 {
		return nil
	}

	g.current = g.Channels.Channels[index]
	must(g.Channels.ChList.SelectRow, g.current.Row)

	return g.current
}

func (g *Guild) GoTo(ch *Channel) error {
	g.current = ch

	if err := ch.loadMessages(); err != nil {
		return errors.Wrap(err, "Failed to load messages")
	}

	return nil
}

func (g *Guild) UpdateImage() {
	if g.IURL == "" {
		return
	}

	err := cache.SetImageScaled(g.IURL+"?size=64", g.Image, IconSize, IconSize, cache.Round)
	if err != nil {
		log.Errorln("Failed to update the pixbuf guild icon:", err)
		return
	}

	must(g.Image.SetHAlign, gtk.ALIGN_CENTER)
	must(g.Image.SetVAlign, gtk.ALIGN_CENTER)
}

func (g *Guild) requestMember(memID discord.Snowflake) {
	g.requestingMemMutex.Lock()
	defer g.requestingMemMutex.Unlock()

	if g.requestingMembers == nil {
		g.requestingMembers = map[discord.Snowflake]struct{}{}
	} else {
		if _, ok := g.requestingMembers[memID]; ok {
			return
		}
	}

	err := App.State.Gateway.RequestGuildMembers(gateway.RequestGuildMembersData{
		GuildID:   []discord.Snowflake{g.ID},
		UserIDs:   []discord.Snowflake{memID},
		Presences: true,
	})

	if err != nil {
		log.Errorln("Failed to request guild members:", err)
	}

	g.requestingMembers[memID] = struct{}{}
	return
}

func (g *Guild) requestedMember(memID discord.Snowflake) {
	g.requestingMemMutex.Lock()
	defer g.requestingMemMutex.Unlock()

	if g.requestingMembers == nil {
		g.requestingMembers = map[discord.Snowflake]struct{}{}
	} else {
		delete(g.requestingMembers, memID)
	}
}

func escape(str string) string {
	return html.EscapeString(str)
}

func bold(str string) string {
	return "<b>" + escape(str) + "</b>"
}
