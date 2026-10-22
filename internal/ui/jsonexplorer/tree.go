// Package jsonexplorer is the built-in `mtm json` explorer: a Bubble Tea tree
// view with fold, fuzzy search, type filter, breadcrumb and a detail panel.
package jsonexplorer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// Kind is the JSON type of a node.
type Kind string

const (
	KString Kind = "string"
	KNumber Kind = "number"
	KBool   Kind = "bool"
	KNull   Kind = "null"
	KObject Kind = "object"
	KArray  Kind = "array"
)

// Node is one value in the tree.
type Node struct {
	Key      string // object key, "[i]" for array items, "$" for root
	Kind     Kind
	Scalar   string // literal text for scalars (strings are quoted)
	Text     string // searchable plain text for scalars (strings unquoted)
	Children []*Node
	Parent   *Node
	Depth    int
	Path     string
	Expanded bool
}

func (n *Node) IsContainer() bool { return n.Kind == KObject || n.Kind == KArray }

// TypeLabel is the value-type text: string, number, bool, null, object{n}, array[n].
func (n *Node) TypeLabel() string {
	switch n.Kind {
	case KObject:
		return fmt.Sprintf("object{%d}", len(n.Children))
	case KArray:
		return fmt.Sprintf("array[%d]", len(n.Children))
	}
	return string(n.Kind)
}

// ParseError reports invalid JSON with a 1-based position.
type ParseError struct {
	Line, Col int
	Msg       string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("invalid JSON at line %d, column %d: %s", e.Line, e.Col, e.Msg)
}

func posError(data []byte, off int64, msg string) *ParseError {
	if off > int64(len(data)) {
		off = int64(len(data))
	}
	if off < 1 {
		off = 1
	}
	head := data[:off]
	line := bytes.Count(head, []byte("\n")) + 1
	col := int(off) - bytes.LastIndexByte(head, '\n')
	return &ParseError{Line: line, Col: col, Msg: msg}
}

// Build parses data into a tree, preserving key order.
func Build(data []byte) (*Node, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, &ParseError{Line: 1, Col: 1, Msg: "empty input"}
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	root, err := parseValue(dec, "$", "$", nil)
	if err != nil {
		return nil, wrapErr(data, dec, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			return nil, posError(data, dec.InputOffset(), "unexpected data after top-level value")
		}
		return nil, wrapErr(data, dec, err)
	}
	root.Expanded = true
	return root, nil
}

func wrapErr(data []byte, dec *json.Decoder, err error) error {
	var se *json.SyntaxError
	switch {
	case errors.As(err, &se):
		return posError(data, se.Offset, se.Error())
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return posError(data, int64(len(data)), "unexpected end of input")
	}
	return posError(data, dec.InputOffset(), err.Error())
}

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func childPath(parent, key string, array bool, idx int) string {
	if array {
		return parent + "[" + strconv.Itoa(idx) + "]"
	}
	if identRe.MatchString(key) {
		return parent + "." + key
	}
	return parent + "[" + strconv.Quote(key) + "]"
}

func parseValue(dec *json.Decoder, key, path string, parent *Node) (*Node, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	n := &Node{Key: key, Parent: parent, Path: path}
	if parent != nil {
		n.Depth = parent.Depth + 1
	}
	switch t := tok.(type) {
	case json.Delim:
		if t == '{' {
			n.Kind = KObject
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k, _ := kt.(string)
				c, err := parseValue(dec, k, childPath(path, k, false, 0), n)
				if err != nil {
					return nil, err
				}
				n.Children = append(n.Children, c)
			}
		} else {
			n.Kind = KArray
			for i := 0; dec.More(); i++ {
				c, err := parseValue(dec, "["+strconv.Itoa(i)+"]", childPath(path, "", true, i), n)
				if err != nil {
					return nil, err
				}
				n.Children = append(n.Children, c)
			}
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
	case string:
		n.Kind, n.Scalar, n.Text = KString, strconv.Quote(t), t
	case json.Number:
		n.Kind, n.Scalar, n.Text = KNumber, t.String(), t.String()
	case bool:
		n.Kind, n.Scalar, n.Text = KBool, strconv.FormatBool(t), strconv.FormatBool(t)
	case nil:
		n.Kind, n.Scalar, n.Text = KNull, "null", "null"
	}
	return n, nil
}

// Flatten returns all nodes in pre-order.
func Flatten(root *Node) []*Node {
	var out []*Node
	var walk func(*Node)
	walk = func(n *Node) {
		out = append(out, n)
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	return out
}

// Pretty renders the node's full value as indented JSON, keeping key order.
func Pretty(n *Node) string {
	var sb strings.Builder
	writePretty(&sb, n, 0)
	return sb.String()
}

func writePretty(sb *strings.Builder, n *Node, ind int) {
	pad := strings.Repeat("  ", ind)
	switch n.Kind {
	case KObject, KArray:
		open, cls := "{", "}"
		if n.Kind == KArray {
			open, cls = "[", "]"
		}
		if len(n.Children) == 0 {
			sb.WriteString(open + cls)
			return
		}
		sb.WriteString(open + "\n")
		for i, c := range n.Children {
			sb.WriteString(pad + "  ")
			if n.Kind == KObject {
				sb.WriteString(strconv.Quote(c.Key) + ": ")
			}
			writePretty(sb, c, ind+1)
			if i < len(n.Children)-1 {
				sb.WriteString(",")
			}
			sb.WriteString("\n")
		}
		sb.WriteString(pad + cls)
	default:
		sb.WriteString(n.Scalar)
	}
}
