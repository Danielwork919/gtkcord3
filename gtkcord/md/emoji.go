package md

import (
	"bytes"
	"regexp"

	"github.com/diamondburned/gtkcord3/gtkcord/cache"
	"github.com/diamondburned/gtkcord3/gtkcord/gtkutils"
	"github.com/diamondburned/gtkcord3/internal/log"
	"github.com/gotk3/gotk3/gtk"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

const (
	InlineEmojiSize = 22
	LargeEmojiSize  = 48
)

func EmojiURL(emojiID string, animated bool) string {
	const EmojiBaseURL = "https://cdn.discordapp.com/emojis/"

	if animated {
		return EmojiBaseURL + emojiID + ".gif"
	}

	return EmojiBaseURL + emojiID + ".png"
}

type Emoji struct {
	ast.BaseInline

	ID   string
	Name string
	GIF  bool

	Large bool // TODO
}

var KindEmoji = ast.NewNodeKind("Emoji")

// Kind implements Node.Kind.
func (e *Emoji) Kind() ast.NodeKind {
	return KindEmoji
}

// Dump implements Node.Dump
func (e *Emoji) Dump(source []byte, level int) {
	ast.DumpHelper(e, source, level, nil, nil)
}

func (e Emoji) EmojiURL() string {
	return EmojiURL(string(e.ID), e.GIF)
}

type emoji struct {
	searched bool // if a small/large check was done
	large    bool
}

var emojiRegex = regexp.MustCompile(`<(a?):(.+?):(\d+)>`)

func (emoji) Trigger() []byte {
	// return []byte("http")
	return []byte{'<'}
}

func (state *emoji) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	match := matchInline(block, '<', '>')
	if match == nil {
		return nil
	}

	var matches = emojiRegex.FindSubmatch(match)
	if len(matches) != 4 {
		return nil
	}

	var emoji = &Emoji{
		BaseInline: ast.BaseInline{},

		GIF:   string(matches[1]) == "a",
		Name:  string(matches[2]),
		ID:    string(matches[3]),
		Large: state.large,
	}

	// Check if emojis should be small:
	if !state.searched {
		state.searched = true

		// Get the entire text:
		source := block.Source()

		// Try and delete all emoji matches:
		source = emojiRegex.ReplaceAll(source, nil)

		// Trim spaces:
		source = bytes.TrimSpace(source)

		// Check if there are letters:
		if len(source) == 0 {
			// No, make our emojis big:
			state.large = true
			emoji.Large = true
		}
	}

	return emoji
}

func (s *TagState) inlineEmojiTag() *gtk.TextTag {
	t, err := s.table.Lookup("emoji")
	if err == nil {
		return t
	}

	t, err = gtk.TextTagNew("emoji")
	if err != nil {
		log.Panicln("Failed to create new emoji tag:", err)
	}

	t.SetProperty("rise", -8192)

	s.table.Add(t)
	return t
}

func (r *Renderer) insertEmoji(e *Emoji) {
	// TODO
	var sz = InlineEmojiSize
	if e.Large {
		sz = LargeEmojiSize
	}

	anchor := r.Buffer.CreateChildAnchor(r.Buffer.GetEndIter())

	img, _ := gtk.ImageNew()
	img.Show()
	img.SetTooltipText(e.Name)
	img.SetSizeRequest(sz, 10) // 10 is the minimum height
	img.SetProperty("yalign", 1.0)
	gtkutils.ImageSetIcon(img, "image-missing", sz)

	r.View.AddChildAtAnchor(img, anchor)

	url := e.EmojiURL() + "?size=64"

	go func() {
		if err := cache.SetImageScaled(url, img, sz, sz); err != nil {
			log.Errorln("Markdown: Failed to GET "+url+":", err)
		}
	}()
}
