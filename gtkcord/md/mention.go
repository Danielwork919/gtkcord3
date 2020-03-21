package md

import (
	"regexp"

	"github.com/diamondburned/arikawa/discord"
	"github.com/gotk3/gotk3/gtk"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

type Mention struct {
	ast.BaseInline

	// both could be nil
	Channel   *discord.Channel
	GuildUser *discord.GuildUser
}

var KindMention = ast.NewNodeKind("Mention")

// Kind implements Node.Kind.
func (m *Mention) Kind() ast.NodeKind {
	return KindMention
}

// Dump implements Node.Dump
func (m *Mention) Dump(source []byte, level int) {
	ast.DumpHelper(m, source, level, nil, nil)
}

type mention struct{}

var mentionRegex = regexp.MustCompile(`<(@!?|@&|#)(\d+)>`)

func (mention) Trigger() []byte {
	return []byte{'<'}
}

func (mention) Parse(parent ast.Node, block text.Reader, pc parser.Context) ast.Node {
	// Don't parse if no messages are given:
	msg := getMessage(pc)
	if msg == nil {
		return nil
	}

	// ALso don't parse if there's no Discord state:
	state := getSession(pc)
	if state == nil {
		return nil
	}

	match := matchInline(block, '<', '>')
	if match == nil {
		return nil
	}

	var matches = mentionRegex.FindSubmatch(match)
	if len(matches) != 3 {
		return nil
	}

	// All of the mentions should have a valid ID:
	d, err := discord.ParseSnowflake(string(matches[2]))
	if err != nil {
		return nil
	}

	switch string(matches[1]) {
	case "#": // channel
		c, err := state.Channel(d)
		if err != nil {
			return nil
		}

		return &Mention{
			BaseInline: ast.BaseInline{},
			Channel:    c,
		}

	case "@", "@!": // user/member
		var target *discord.GuildUser
		for _, user := range msg.Mentions {
			if user.ID == d {
				target = &user
				break
			}
		}

		// If we can't find the user mentioned, then it's a bad mention.
		if target == nil {
			return nil
		}

		// Fill up the Member field if we can:
		if msg.GuildID.Valid() && msg.Member == nil {
			m, err := state.Member(msg.GuildID, target.ID)
			if err == nil {
				target.Member = m
			}
		}

		return &Mention{
			BaseInline: ast.BaseInline{},
			GuildUser:  target,
		}

	case "@&": // role
		// TODO
		return nil
	}

	return nil
}

func (s *TagState) guildUser(gu *discord.GuildUser) *gtk.TextTag {
	if UserPressed == nil {
		return nil
	}

	return s.addHandler("@"+gu.ID.String(), func(ev PressedEvent) {
		UserPressed(ev, gu)
	})
}

func (s *TagState) channel(ch *discord.Channel) *gtk.TextTag {
	if ChannelPressed == nil {
		return nil
	}

	return s.addHandler("#"+ch.ID.String(), func(ev PressedEvent) {
		ChannelPressed(ev, ch)
	})
}
