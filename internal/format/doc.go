package format

import (
	"bytes"
	"fmt"
	"strings"
)

type docKind uint8

const (
	docEmpty docKind = iota
	docText
	docLine
	docSoftLine
	docConcat
	docIndent
	docGroup
)

// Doc is a formatter document node.
type Doc struct {
	kind  docKind
	text  string
	child *Doc
	list  []Doc
}

// Empty returns an empty document.
func Empty() Doc { return Doc{kind: docEmpty} }

// Text returns a text document.
func Text(s string) Doc {
	if s == "" {
		return Empty()
	}
	return Doc{kind: docText, text: s}
}

// Line returns a hard line break document.
func Line() Doc { return Doc{kind: docLine} }

// SoftLine returns a breakable line that renders as a space when flattened.
func SoftLine() Doc { return Doc{kind: docSoftLine} }

// Concat concatenates documents in order.
func Concat(parts ...Doc) Doc {
	filtered := make([]Doc, 0, len(parts))
	for _, p := range parts {
		if p.kind == docEmpty {
			continue
		}
		if p.kind == docConcat {
			filtered = append(filtered, p.list...)
			continue
		}
		filtered = append(filtered, p)
	}
	switch len(filtered) {
	case 0:
		return Empty()
	case 1:
		return filtered[0]
	default:
		return Doc{kind: docConcat, list: filtered}
	}
}

// Indent increases indentation for nested line breaks.
func Indent(doc Doc) Doc {
	if doc.kind == docEmpty {
		return doc
	}
	return Doc{kind: docIndent, child: &doc}
}

// Group attempts to render doc on one line and falls back to line breaks when needed.
func Group(doc Doc) Doc {
	if doc.kind == docEmpty {
		return doc
	}
	return Doc{kind: docGroup, child: &doc}
}

// RenderOptions configure doc rendering.
type RenderOptions struct {
	LineWidth int
	Indent    string
	Newline   string
}

type renderMode uint8

const (
	modeBreak renderMode = iota
	modeFlat
)

type renderFrame struct {
	indent int
	mode   renderMode
	doc    Doc
}

// Render renders doc into bytes using width-aware grouping.
func Render(doc Doc, opts RenderOptions) ([]byte, error) {
	norm, err := normalizeRenderOptions(opts)
	if err != nil {
		return nil, err
	}

	var out bytes.Buffer
	column := 0
	stack := []renderFrame{{indent: 0, mode: modeBreak, doc: doc}}

	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		switch f.doc.kind {
		case docEmpty:
			continue
		case docText:
			out.WriteString(f.doc.text)
			column += len(f.doc.text)
		case docLine:
			writeLineBreak(&out, norm.Newline, norm.Indent, f.indent)
			column = f.indent * len(norm.Indent)
		case docSoftLine:
			if f.mode == modeFlat {
				out.WriteByte(' ')
				column++
				continue
			}
			writeLineBreak(&out, norm.Newline, norm.Indent, f.indent)
			column = f.indent * len(norm.Indent)
		case docConcat:
			pushConcat(&stack, f.indent, f.mode, f.doc.list)
		case docIndent:
			if f.doc.child != nil {
				stack = append(stack, renderFrame{indent: f.indent + 1, mode: f.mode, doc: *f.doc.child})
			}
		case docGroup:
			if f.doc.child == nil {
				continue
			}
			child := *f.doc.child
			mode := f.mode
			if mode == modeBreak && fits(norm.LineWidth-column, stack, renderFrame{indent: f.indent, mode: modeFlat, doc: child}) {
				mode = modeFlat
			}
			stack = append(stack, renderFrame{indent: f.indent, mode: mode, doc: child})
		default:
			return nil, fmt.Errorf("unknown doc kind %d", f.doc.kind)
		}
	}

	return out.Bytes(), nil
}

func normalizeRenderOptions(opts RenderOptions) (RenderOptions, error) {
	base, err := normalizeOptions(Options{
		LineWidth: opts.LineWidth,
		Indent:    opts.Indent,
	})
	if err != nil {
		return RenderOptions{}, err
	}
	if opts.Newline == "" {
		opts.Newline = "\n"
	}
	if opts.Newline != "\n" && opts.Newline != "\r\n" {
		return RenderOptions{}, fmt.Errorf("invalid newline %q", opts.Newline)
	}
	opts.LineWidth = base.LineWidth
	opts.Indent = base.Indent
	return opts, nil
}

func writeLineBreak(out *bytes.Buffer, newline, indent string, indentLevel int) {
	out.WriteString(newline)
	out.WriteString(strings.Repeat(indent, indentLevel))
}

func pushConcat(stack *[]renderFrame, indent int, mode renderMode, list []Doc) {
	for i := len(list) - 1; i >= 0; i-- {
		*stack = append(*stack, renderFrame{indent: indent, mode: mode, doc: list[i]})
	}
}

func fits(width int, tail []renderFrame, first renderFrame) bool {
	if width < 0 {
		return false
	}
	stack := make([]renderFrame, 0, len(tail)+1)
	stack = append(stack, tail...)
	stack = append(stack, first)

	for len(stack) > 0 {
		if width < 0 {
			return false
		}
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		switch f.doc.kind {
		case docEmpty:
			continue
		case docText:
			width -= len(f.doc.text)
		case docLine:
			return true
		case docSoftLine:
			if f.mode == modeFlat {
				width--
				continue
			}
			return true
		case docConcat:
			pushConcat(&stack, f.indent, f.mode, f.doc.list)
		case docIndent:
			if f.doc.child != nil {
				stack = append(stack, renderFrame{indent: f.indent + 1, mode: f.mode, doc: *f.doc.child})
			}
		case docGroup:
			if f.doc.child != nil {
				stack = append(stack, renderFrame{indent: f.indent, mode: modeFlat, doc: *f.doc.child})
			}
		}
	}

	return width >= 0
}
