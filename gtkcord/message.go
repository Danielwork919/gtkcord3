package gtkcord

import (
	"fmt"
	"sync"
	"time"

	"github.com/diamondburned/arikawa/discord"
	"github.com/diamondburned/arikawa/state"
	"github.com/diamondburned/gtkcord3/gtkcord/md"
	"github.com/diamondburned/gtkcord3/gtkcord/pbpool"
	"github.com/diamondburned/gtkcord3/gtkcord/semaphore"
	"github.com/gotk3/gotk3/gtk"
	"github.com/pkg/errors"
)

const (
	DefaultFetch  = 25
	AvatarSize    = 42 // gtk.ICON_SIZE_DND
	AvatarPadding = 10
)

type Messages struct {
	ExtendedWidget
	Main      *gtk.Box
	Scroll    *gtk.ScrolledWindow
	Viewport  *gtk.Viewport
	ChannelID discord.Snowflake
	Messages  []*Message
	guard     sync.Mutex
}

func (m *Messages) Reset(s *state.State, parser *md.Parser) error {
	m.guard.Lock()
	defer m.guard.Unlock()

	if m.Main == nil {
		b, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
		if err != nil {
			return errors.Wrap(err, "Failed to make box")
		}
		b.SetVExpand(true)
		b.SetHExpand(true)
		m.Main = b

		v, err := gtk.ViewportNew(nil, nil)
		if err != nil {
			return errors.Wrap(err, "Failed to create viewport")
		}
		must(v.Add, b)
		m.Viewport = v

		s, err := gtk.ScrolledWindowNew(nil, nil)
		if err != nil {
			return errors.Wrap(err, "Failed to create channel scroller")
		}
		s.SetPolicy(gtk.POLICY_NEVER, gtk.POLICY_ALWAYS)
		m.ExtendedWidget = s
		m.Scroll = s

		must(s.Add, v)
	}

	for _, w := range m.Messages {
		must(m.Main.Remove, w)
	}

	// Order: latest is first.
	messages, err := s.Messages(m.ChannelID)
	if err != nil {
		return errors.Wrap(err, "Failed to get messages")
	}

	var newMessages = m.Messages[:0]

	// Iterate from earliest to latest.
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]

		w, err := newMessage(s, parser, message)
		if err != nil {
			return errors.Wrap(err, "Failed to render message")
		}

		must(m.Main.Add, w)
		// Messages are added, earliest first.
		newMessages = append(newMessages, w)
	}

	m.Messages = newMessages

	must(m.Main.ShowAll)

	go func() {
		// Revert to latest is last, earliest is first.
		for L, R := 0, len(messages)-1; L < R; L, R = L+1, R-1 {
			messages[L], messages[R] = messages[R], messages[L]
		}

		// Iterate in reverse, so latest first.
		for i := len(newMessages) - 1; i >= 0; i-- {
			message, discordm := newMessages[i], messages[i]
			message.UpdateAuthor(discordm.Author)
			message.UpdateExtras(discordm)

			must(message.ShowAll)
		}

		m.SmartScroll()
	}()

	m.SmartScroll()
	return nil
}

func (m *Messages) Insert(s *state.State, parser *md.Parser, message discord.Message) error {
	w, err := newMessage(s, parser, message)
	if err != nil {
		return errors.Wrap(err, "Failed to render message")
	}

	semaphore.Go(func() {
		w.UpdateAuthor(message.Author)
		w.UpdateExtras(message)
	})

	m.guard.Lock()
	defer m.guard.Unlock()

	must(m.Main.Add, w)
	must(m.Main.ShowAll)
	m.Messages = append(m.Messages, w)
	m.SmartScroll()
	return nil
}

func (m *Messages) Update(s *state.State, parser *md.Parser, update discord.Message) {
	var target *Message

	m.guard.Lock()
	for _, message := range m.Messages {
		if message.ID == update.ID {
			target = message
		}
	}
	m.guard.Unlock()

	if target == nil {
		return
	}
	if update.Content != "" {
		target.UpdateContent(update)
	}
	semaphore.Go(func() {
		target.UpdateExtras(update)
	})
}

func (m *Messages) Delete(id discord.Snowflake) bool {
	m.guard.Lock()
	defer m.guard.Unlock()

	for i, message := range m.Messages {
		if message.ID != id {
			continue
		}

		m.Messages = append(m.Messages[:i], m.Messages[i+1:]...)
		m.Main.Remove(message)
		return true
	}

	return false
}

func (m *Messages) SmartScroll() {
	// Fuck GTK, world is a fuck.
	time.Sleep(15 * time.Millisecond)

	must(func() {
		adj, err := m.Viewport.GetVAdjustment()
		if err != nil {
			logWrap(err, "Failed to get viewport")
			return
		}

		max := adj.GetUpper()
		cur := adj.GetValue()

		// If the user has scrolled past 10% from the bottom:
		if (max-cur)/max < 0.1 {
			return
		}

		adj.SetValue(max)
		m.Viewport.SetVAdjustment(adj)
	})
}

type Message struct {
	ExtendedWidget

	ID    discord.Snowflake
	Guild discord.Snowflake

	State  *state.State
	Parser *md.Parser

	Main *gtk.Box

	// Left side:
	Avatar *gtk.Image
	Pixbuf *Pixbuf
	PbURL  string

	// Right container:
	Right *gtk.Box

	// Right-top container, has author and time:
	RightTop  *gtk.Box
	Author    *gtk.Label
	Timestamp *gtk.Label

	// Right-bottom container, has message contents:
	RightBottom *gtk.Box
	Content     *gtk.TextBuffer  // view declared implicitly
	Extras      []*MessageExtras // embeds, images, etc
}

type MessageExtras struct {
	gtk.IWidget
}

func newMessage(s *state.State, parser *md.Parser, m discord.Message) (*Message, error) {
	main, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 0)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to create main box")
	}

	right, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to create right box")
	}

	avatar, err := gtk.ImageNewFromIconName("user-info", gtk.ICON_SIZE_DND)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to create avatar user-info")
	}

	rtop, err := gtk.BoxNew(gtk.ORIENTATION_HORIZONTAL, 0)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to create right top box")
	}
	rbottom, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to create right bottom box")
	}

	author, err := gtk.LabelNew("")
	if err != nil {
		return nil, errors.Wrap(err, "Failed to create author label")
	}
	timestamp, err := gtk.LabelNew("")
	if err != nil {
		return nil, errors.Wrap(err, "Failed to create timestamp label")
	}

	ttt, err := gtk.TextTagTableNew()
	if err != nil {
		return nil, errors.Wrap(err, "Faield to create a text tag table")
	}

	msgTb, err := gtk.TextBufferNew(ttt)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to create a text buffer")
	}

	// What the fuck?
	must(func() bool {
		margin(&main.Widget, 15)

		avatar.SetSizeRequest(AvatarSize, AvatarSize)
		avatar.SetProperty("yalign", 0.0)
		avatar.SetMarginEnd(AvatarPadding)
		main.Add(avatar)

		main.Add(right)

		right.Add(rtop)
		rbottom.SetHExpand(true)
		right.Add(rbottom)

		author.SetMarkup(bold(m.Author.Username))
		rtop.Add(author)

		timestamp.SetMarkup(
			`<span font_size="smaller">` + m.Timestamp.Format(time.Kitchen) + "</span>")
		timestamp.SetOpacity(0.75)
		timestamp.SetMarginStart(10)
		rtop.Add(timestamp)

		msgTv, err := gtk.TextViewNewWithBuffer(msgTb)
		if err != nil {
			panic("Die: " + err.Error())
		}
		msgTv.SetWrapMode(gtk.WRAP_WORD)
		msgTv.SetCursorVisible(false)
		msgTv.SetEditable(false)
		rbottom.Add(msgTv)

		return false
	})

	message := Message{
		ID:             m.ID,
		Guild:          m.GuildID,
		ExtendedWidget: main,
		State:          s,
		Parser:         parser,
		Main:           main,
		Avatar:         avatar,
		Right:          right,
		RightTop:       rtop,
		Author:         author,
		Timestamp:      timestamp,
		RightBottom:    rbottom,
		Content:        msgTb,
	}

	message.UpdateContent(m)

	return &message, nil
}

func (m *Message) UpdateAuthor(user discord.User) {
	if m.Guild.Valid() {
		var name = user.Username

		n, err := m.State.MemberDisplayName(m.Guild, user.ID)
		if err == nil {
			name = bold(escape(n))

			if color := m.State.MemberColor(m.Guild, user.ID); color > 0 {
				name = fmt.Sprintf(`<span fgcolor="#%06X">%s</span>`, color, name)
			}
		}

		must(m.Author.SetMarkup, name)
	}

	var url = user.AvatarURL()
	var animated = url[:len(url)-4] == ".gif"

	if m.PbURL == url {
		return
	}
	m.PbURL = url

	if !animated {
		p, err := pbpool.GetScaled(url+"?size=64", AvatarSize, AvatarSize, pbpool.Round)
		if err != nil {
			// logWrap(err, "Failed to get the pixbuf guild icon")
			return
		}

		m.Pixbuf = &Pixbuf{p, nil}
		m.Pixbuf.Set(m.Avatar)
	} else {
		p, err := pbpool.GetAnimationScaled(url+"?size=64", AvatarSize, AvatarSize, pbpool.Round)
		if err != nil {
			// logWrap(err, "Failed to get the pixbuf guild animation")
			return
		}

		m.Pixbuf = &Pixbuf{nil, p}
		m.Pixbuf.Set(m.Avatar)
	}
}

func (m *Message) UpdateContent(update discord.Message) {
	m.Content.Delete(m.Content.GetStartIter(), m.Content.GetEndIter())
	m.Parser.ParseMessage(&update, []byte(update.Content), m.Content)
}

func (m *Message) UpdateExtras(update discord.Message) {
	// TODO
}
