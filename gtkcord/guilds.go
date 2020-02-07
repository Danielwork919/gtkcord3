package gtkcord

import (
	"fmt"
	"io/ioutil"

	"github.com/diamondburned/arikawa/discord"
	"github.com/diamondburned/arikawa/gateway"
	"github.com/diamondburned/arikawa/state"
	"github.com/diamondburned/gtkcord3/gtkcord/icons"
	"github.com/gotk3/gotk3/gdk"
	"github.com/gotk3/gotk3/glib"
	"github.com/gotk3/gotk3/gtk"
	"github.com/pkg/errors"
)

const (
	FolderSize  = 36
	IconSize    = 56
	IconPadding = 6
)

type Guilds struct {
	*gtk.TreeView
	Store *gtk.TreeStore

	Friends *gtk.TreeIter // TODO
	Guilds  []*Guild
}

type Guild struct {
	*gtk.TreeIter
	Folder *GuildFolder // can be non-nil

	Parent *gtk.TreeIter
	Iter   *gtk.TreeIter
	Store  *gtk.TreeStore

	// nil if not downloaded
	Pixbuf    *gdk.Pixbuf
	Animation *gdk.PixbufAnimation

	ID   discord.Snowflake
	Name string

	// nil if Folder
	Channels *Channels
}

type GuildFolder struct {
	Guilds []*Guild
}

func (a *Application) newGuilds(s *state.State) (*Guilds, error) {
	// Prefetch guilds:
	_, err := s.Guilds()
	if err != nil {
		return nil, errors.Wrap(err, "Failed to get guilds")
	}

	tv, err := gtk.TreeViewNew()
	if err != nil {
		return nil, errors.Wrap(err, "Failed to create the guild tree")
	}

	cr, err := gtk.CellRendererPixbufNew()
	if err != nil {
		return nil, errors.Wrap(err, "Failed to create cell renderer")
	}

	cl, err := gtk.TreeViewColumnNewWithAttribute("", cr, "pixbuf", 0)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to create tree column")
	}

	tv.AppendColumn(cl)

	ts, err := gtk.TreeStoreNew(glib.TYPE_OBJECT)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to create guild tree store")
	}
	tv.SetModel(ts)

	var folders = s.Ready.Settings.GuildFolders
	var rows = make([]*Guild, 0, len(folders))

	for _, f := range folders {
		switch len(f.GuildIDs) {
		case 0: // ???
			continue
		case 1:
			g, err := s.Guild(f.GuildIDs[0])
			if err != nil {
				return nil,
					errors.Wrap(err, "Failed to get guild in folder "+f.Name)
			}

			r, err := a.newGuildRow(ts, nil, *g)
			if err != nil {
				return nil,
					errors.Wrap(err, "Failed to load guild "+g.Name)
			}

			rows = append(rows, r)

		default:
			e, err := a.newGuildFolder(s, ts, f)
			if err != nil {
				return nil,
					errors.Wrap(err, "Failed to create a new folder "+f.Name)
			}

			rows = append(rows, e)
		}
	}

	tv.ShowAll()

	return &Guilds{
		TreeView: tv,
		Store:    ts,
		Guilds:   rows,
	}, nil
}

/*
func (g *Guild) Add(store *gtk.TreeStore) {
	iter := store.Append(nil)
	store.SetValue(iter, 0, g.Image)
	g.TreeIter = iter

	if g.Folder != nil {
		g.Folder.Parent = iter

		for _, c := range g.Folder.Guilds {
			child := store.Append(iter)
			must(func() {
				store.SetValue(child, 0, c.Image)
			}, child, 0, c.Image)
			c.TreeIter = child
		}
	}
}
*/

func (a *Application) newGuildFolder(
	s *state.State,
	store *gtk.TreeStore, folder gateway.GuildFolder) (*Guild, error) {

	if folder.Color == 0 {
		folder.Color = 0x7289DA
	}

	p, err := icons.PixbufIcon(icons.Folder(folder.Color.Uint32()), FolderSize)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to create the folder pixbuf")
	}

	f := &Guild{
		Iter: store.Append(nil),
		Folder: &GuildFolder{
			Guilds: make([]*Guild, 0, len(folder.GuildIDs)),
		},
		Pixbuf: p,

		ID:   folder.ID,
		Name: folder.Name,
	}
	f.UpdateStore()

	for _, id := range folder.GuildIDs {
		g, err := s.Guild(id)
		if err != nil {
			return nil, errors.Wrap(err, "Failed to get guild ID"+id.String())
		}

		r, err := a.newGuildRow(store, f.Iter, *g)
		if err != nil {
			return nil, errors.Wrap(err, "Failed to load guild "+g.Name)
		}

		f.Folder.Guilds = append(f.Folder.Guilds, r)
	}

	return f, nil
}

func (a *Application) newGuildRow(
	store *gtk.TreeStore,
	parent *gtk.TreeIter, guild discord.Guild) (*Guild, error) {

	/*
		// Set paddings:
		r.SetSizeRequest(IconSize+IconPadding*2, IconSize+IconPadding*2)
		r.SetHAlign(gtk.ALIGN_CENTER)
		r.SetVAlign(gtk.ALIGN_CENTER)
		r.SetTooltipText(guild.Name)
	*/

	g := &Guild{
		Iter:   store.Append(parent),
		ID:     guild.ID,
		Name:   guild.Name,
		Store:  store,
		Parent: parent,
	}

	i, err := a.iconTheme.LoadIcon("image-loading", 48, 0)
	if err == nil {
		p, err := i.ApplyEmbeddedOrientation()
		if err != nil {
			return nil, errors.Wrap(err, "Failed to convert icon to pixbuf")
		}

		g.Pixbuf = p
	}

	g.UpdateStore()

	var url = guild.IconURL()
	if url == "" {
		// Guild doesn't have an icon, exit:
		return g, nil
	}

	var animated = url[:len(url)-4] == ".gif"

	go func() {
		r, err := HTTPClient.Get(url + "?size=64")
		if err != nil {
			logWrap(err, "Failed to GET URL "+url)
			return
		}
		defer r.Body.Close()

		if r.StatusCode < 200 || r.StatusCode > 299 {
			logError(fmt.Errorf("Bad status code %d for %s", r.StatusCode, url))
		}

		b, err := ioutil.ReadAll(r.Body)
		if err != nil {
			logWrap(err, "Failed to download image")
			return
		}

		if animated {
			p, err := NewPixbuf(b, PbSize(IconSize, IconSize))
			if err != nil {
				logWrap(err, "Failed to get the pixbuf guild icon")
				return
			}

			g.Pixbuf = p
			g.UpdateStore()
		} else {
			p, err := NewAnimator(b, PbSize(IconSize, IconSize))
			if err != nil {
				logWrap(err, "Failed to get the pixbuf guild animation")
			}

			g.Animation = p
			g.UpdateStore()
		}
	}()

	return g, nil
}

func (g *Guild) UpdateStore() {
	switch {
	case g.Pixbuf != nil:
		must(func(pb gdk.Pixbuf) {
			g.Store.SetValue(g.Iter, 0, pb)
		}, *g.Pixbuf)
	case g.Animation != nil:
		must(func(pb gdk.PixbufAnimation) {
			g.Store.SetValue(g.Iter, 0, pb)
		}, *g.Animation)
	}
}
