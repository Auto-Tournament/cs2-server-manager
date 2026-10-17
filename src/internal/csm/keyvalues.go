package csm

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// kvNode is one entry of a Valve KeyValues (VDF) text file such as
// gamemodes.txt or appmanifest_730.acf. A node is either a leaf with a Value
// or a block with Children.
type kvNode struct {
	Key      string
	Value    string
	IsBlock  bool
	Children []*kvNode
}

// child returns the first direct child whose key matches (case-insensitive).
func (n *kvNode) child(key string) *kvNode {
	for _, c := range n.Children {
		if strings.EqualFold(c.Key, key) {
			return c
		}
	}
	return nil
}

type kvTokenKind int

const (
	kvString kvTokenKind = iota
	kvOpen
	kvClose
)

type kvToken struct {
	kind kvTokenKind
	text string
	line int
}

// tokenizeKeyValues splits KeyValues text into strings and braces. It skips
// // comments and [$PLATFORM] conditionals.
func tokenizeKeyValues(src string) ([]kvToken, error) {
	src = strings.TrimPrefix(src, "\ufeff")
	var toks []kvToken
	line := 1
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == '\n':
			line++
			i++
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '{':
			toks = append(toks, kvToken{kind: kvOpen, line: line})
			i++
		case c == '}':
			toks = append(toks, kvToken{kind: kvClose, line: line})
			i++
		case c == '[':
			end := strings.IndexByte(src[i:], ']')
			if end < 0 {
				return nil, fmt.Errorf("line %d: unterminated conditional", line)
			}
			i += end + 1
		case c == '"':
			start := line
			var sb strings.Builder
			i++
			closed := false
			for i < len(src) {
				ch := src[i]
				if ch == '\\' && i+1 < len(src) {
					switch src[i+1] {
					case 'n':
						sb.WriteByte('\n')
					case 't':
						sb.WriteByte('\t')
					case '"', '\\':
						sb.WriteByte(src[i+1])
					default:
						sb.WriteByte('\\')
						sb.WriteByte(src[i+1])
					}
					i += 2
					continue
				}
				if ch == '"' {
					closed = true
					i++
					break
				}
				if ch == '\n' {
					line++
				}
				sb.WriteByte(ch)
				i++
			}
			if !closed {
				return nil, fmt.Errorf("line %d: unterminated string", start)
			}
			toks = append(toks, kvToken{kind: kvString, text: sb.String(), line: start})
		default:
			j := i
			for j < len(src) && !strings.ContainsRune(" \t\r\n{}\"[", rune(src[j])) {
				j++
			}
			toks = append(toks, kvToken{kind: kvString, text: src[i:j], line: line})
			i = j
		}
	}
	return toks, nil
}

// parseKeyValues parses KeyValues text into its top-level nodes.
func parseKeyValues(src string) ([]*kvNode, error) {
	toks, err := tokenizeKeyValues(src)
	if err != nil {
		return nil, err
	}
	pos := 0
	var parseList func(inBlock bool) ([]*kvNode, error)
	parseList = func(inBlock bool) ([]*kvNode, error) {
		var nodes []*kvNode
		for pos < len(toks) {
			t := toks[pos]
			pos++
			switch t.kind {
			case kvClose:
				if !inBlock {
					return nil, fmt.Errorf("line %d: unexpected }", t.line)
				}
				return nodes, nil
			case kvOpen:
				return nil, fmt.Errorf("line %d: unexpected { without a key", t.line)
			}
			if pos >= len(toks) {
				return nil, fmt.Errorf("line %d: key %q has no value", t.line, t.text)
			}
			v := toks[pos]
			pos++
			switch v.kind {
			case kvString:
				nodes = append(nodes, &kvNode{Key: t.text, Value: v.text})
			case kvOpen:
				children, err := parseList(true)
				if err != nil {
					return nil, err
				}
				nodes = append(nodes, &kvNode{Key: t.text, IsBlock: true, Children: children})
			default:
				return nil, fmt.Errorf("line %d: key %q has no value", t.line, t.text)
			}
		}
		if inBlock {
			return nil, fmt.Errorf("unexpected end of file: missing }")
		}
		return nodes, nil
	}
	return parseList(false)
}

// findKV walks nodes depth-first and returns the first node for which match
// is true.
func findKV(nodes []*kvNode, match func(*kvNode) bool) *kvNode {
	for _, n := range nodes {
		if match(n) {
			return n
		}
		if n.IsBlock {
			if f := findKV(n.Children, match); f != nil {
				return f
			}
		}
	}
	return nil
}

// ParseActiveDutyMaps returns CS2's current Active Duty pool from a
// gamemodes.txt.
//
// The pool is the map groups that are both offered in competitive (the
// "competitive" block of "templates#include") and tagged "grouptype"
// "active", in the template's order. That is how the game's own play menu
// builds its "Active Duty" quick-select. The "mg_active" map group is not
// used for this: Valve stopped updating it (in 2026-10 it still listed Train,
// Overpass and Vertigo). Only when the template gives no maps (an older
// gamemodes.txt) does this fall back to "mg_active".
func ParseActiveDutyMaps(gamemodes string) ([]string, error) {
	nodes, err := parseKeyValues(gamemodes)
	if err != nil {
		return nil, fmt.Errorf("parse gamemodes.txt: %w", err)
	}
	if maps := competitiveActiveMaps(nodes); len(maps) > 0 {
		return maps, nil
	}
	// gameTypes also mentions mg_active as a plain string ("mg_active" ""),
	// so look for the block that has a "maps" block inside it.
	group := findKV(nodes, func(n *kvNode) bool {
		return n.IsBlock && strings.EqualFold(n.Key, "mg_active") && mapsBlock(n) != nil
	})
	if group == nil {
		return nil, fmt.Errorf("gamemodes.txt has no Active Duty maps (no competitive template with active map groups, no mg_active map group)")
	}
	var maps []string
	for _, m := range mapsBlock(group).Children {
		if id := strings.TrimSpace(m.Key); id != "" {
			maps = append(maps, id)
		}
	}
	return maps, nil
}

// mapsBlock returns the "maps" block of a map group, or nil.
func mapsBlock(group *kvNode) *kvNode {
	if m := group.child("maps"); m != nil && m.IsBlock {
		return m
	}
	return nil
}

// competitiveActiveMaps returns the maps of the map groups listed in the
// competitive template that have "grouptype" "active", ordered by the
// template's numbers. Lobby groups (mg_lobby_mapveto, "competitivemod"
// "lobby") are skipped.
func competitiveActiveMaps(nodes []*kvNode) []string {
	tmpl := findKV(nodes, func(n *kvNode) bool {
		return n.IsBlock && strings.EqualFold(n.Key, "templates#include")
	})
	if tmpl == nil {
		return nil
	}
	comp := tmpl.child("competitive")
	if comp == nil || !comp.IsBlock {
		return nil
	}
	mapgroups := findKV(nodes, func(n *kvNode) bool {
		return n.IsBlock && strings.EqualFold(n.Key, "mapgroups")
	})
	if mapgroups == nil {
		return nil
	}

	entries := make([]*kvNode, 0, len(comp.Children))
	for _, c := range comp.Children {
		if !c.IsBlock {
			entries = append(entries, c)
		}
	}
	order := func(n *kvNode) int {
		v, err := strconv.Atoi(strings.TrimSpace(n.Value))
		if err != nil {
			return math.MaxInt
		}
		return v
	}
	sort.SliceStable(entries, func(i, j int) bool { return order(entries[i]) < order(entries[j]) })

	var maps []string
	seen := map[string]bool{}
	for _, e := range entries {
		group := mapgroups.child(strings.TrimSpace(e.Key))
		if group == nil || !group.IsBlock {
			continue
		}
		if gt := group.child("grouptype"); gt == nil || !strings.EqualFold(strings.TrimSpace(gt.Value), "active") {
			continue
		}
		if mod := group.child("competitivemod"); mod != nil && strings.EqualFold(strings.TrimSpace(mod.Value), "lobby") {
			continue
		}
		mb := mapsBlock(group)
		if mb == nil {
			continue
		}
		for _, m := range mb.Children {
			if id := strings.TrimSpace(m.Key); id != "" && !seen[id] {
				seen[id] = true
				maps = append(maps, id)
			}
		}
	}
	return maps
}

// parseBuildID returns AppState.buildid from a Steam appmanifest (.acf).
func parseBuildID(acf string) string {
	nodes, err := parseKeyValues(acf)
	if err != nil {
		return ""
	}
	n := findKV(nodes, func(n *kvNode) bool {
		return !n.IsBlock && strings.EqualFold(n.Key, "buildid")
	})
	if n == nil {
		return ""
	}
	return strings.TrimSpace(n.Value)
}

// parsePatchVersion returns PatchVersion from game/csgo/steam.inf.
func parsePatchVersion(steamInf string) string {
	for _, line := range strings.Split(steamInf, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && strings.EqualFold(strings.TrimSpace(k), "PatchVersion") {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
