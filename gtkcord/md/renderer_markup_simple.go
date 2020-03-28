package md

import (
	"bytes"
	"io"

	"github.com/yuin/goldmark/ast"
)

type SimpleMarkupRenderer struct {
	MarkupRenderer
}

func NewSimpleMarkupRenderer() *SimpleMarkupRenderer {
	return &SimpleMarkupRenderer{}
}

func (r *SimpleMarkupRenderer) Render(w io.Writer, source []byte, n ast.Node) error {
	ast.Walk(n, func(n ast.Node, enter bool) (ast.WalkStatus, error) {
		switch n := n.(type) {
		case *Inline:
			r.setAttr(w, n.Attr, enter)
		default:
			r.MarkupRenderer.switchNode(w, n, source, enter)
		}

		return ast.WalkContinue, nil
	})

	return nil
}

func (r *SimpleMarkupRenderer) setAttr(w io.Writer, attr Attribute, enter bool) {
	// close the original span if there's one
	r.closeAttr(w)

	// add/remove to tag
	if enter {
		r.attr.Add(attr)
	} else {
		r.attr.Remove(attr)
	}

	// generate a new span if needed
	r.openAttr(w)
}

func (r *SimpleMarkupRenderer) closeAttr(w io.Writer) {
	if r.attr == 0 {
		return
	}

	var tokens = make([][]byte, 0, 3)

	if r.attr.Has(AttrUnderline) {
		tokens = append(tokens, []byte("</u>"))
	}
	if r.attr.Has(AttrItalics) {
		tokens = append(tokens, []byte("</i>"))
	}
	if r.attr.Has(AttrBold) {
		tokens = append(tokens, []byte("</b>"))
	}

	w.Write(bytes.Join(tokens, nil))
}

func (r *SimpleMarkupRenderer) openAttr(w io.Writer) {
	if r.attr == 0 {
		return
	}

	var tokens = make([][]byte, 0, 3)

	if r.attr.Has(AttrBold) {
		tokens = append(tokens, []byte("<b>"))
	}
	if r.attr.Has(AttrItalics) {
		tokens = append(tokens, []byte("<i>"))
	}
	if r.attr.Has(AttrUnderline) {
		tokens = append(tokens, []byte("<u>"))
	}

	w.Write(bytes.Join(tokens, nil))
}
