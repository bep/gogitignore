// Copyright 2026 Bjørn Erik Pedersen
// SPDX-License-Identifier: MIT

// Package gogitignore implements gitignore-style path matching, organized
// as a tree of matchers keyed by directory. Matchers cascade from outer
// (root) to inner (sub-directory) so an inner pattern can negate one set
// by an outer pattern, mirroring how Git evaluates nested .gitignore files.
package gogitignore

import (
	"strings"
	"sync"

	"github.com/gobwas/glob"
	"github.com/gohugoio/go-radix"
)

// Tree holds a hierarchy of gitignore matchers keyed by the directory the
// matcher applies to.
type Tree struct {
	// Keys are Unix-style paths with a leading and trailing slash, e.g.
	// "/" for the root and "/sub/" for a sub-directory. The trailing
	// slash is what keeps "/foo/" from spuriously prefix-matching
	// "/foobar/x" when we look up the longest prefix.
	tree *radix.Tree[Matcher]

	mu sync.RWMutex
}

func New() *Tree {
	return &Tree{
		tree: radix.New[Matcher](),
	}
}

// AddPatterns parses patterns as gitignore lines and stores the resulting
// matcher at the given path. The path is the directory the patterns are
// relative to (e.g. "/" for root, "/sub" for a sub-directory). Replaces
// any existing matcher at that path.
//
// The empty path "" is reserved for global patterns: they apply to every
// path in the tree and are evaluated before any in-tree .gitignore, so an
// in-tree pattern (negation included) can override a global one. This is
// the slot for patterns sourced from e.g. core.excludesFile; reading those
// from disk is the caller's responsibility.
func (t *Tree) AddPatterns(pth string, patterns ...string) {
	t.AddMatcher(pth, parsePatternList(patterns))
}

// AddMatcher inserts m into the tree at the given path. See AddPatterns
// for path semantics.
func (t *Tree) AddMatcher(pth string, m Matcher) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.tree.Insert(normalizeBase(pth), m)
}

// Match reports whether pth should be ignored. pth is a leading-slash, slash-separated path (e.g. "/foo/bar.txt") relative to the tree root.
// isDir reports whether pth represents a directory.
//
// Match walks the tree from root down to pth, applying each .gitignore
// matcher in turn. For every matcher it considers not just pth but every
// intermediate directory between the matcher's base and pth, so that a path
// inside an excluded directory is treated as ignored regardless of any
// later !-negation that would otherwise re-include it.
func (t *Tree) Match(pth string, isDir bool) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	pth = normalizePath(pth)
	if pth == "/" {
		return false
	}

	// pth has the form "/a/b/.../x". Each "/" introduces a level: level 0
	// is the first component (an ancestor or pth itself if pth has only
	// one component), level n-1 is pth itself.
	n := strings.Count(pth, "/")
	var stack [16]bool
	var ignored []bool
	if n <= len(stack) {
		ignored = stack[:n]
	} else {
		ignored = make([]bool, n)
	}

	// Bases yielded by WalkPath are visited in strictly increasing depth.
	// After processing a matcher at depth d, no later matcher can affect
	// levels [0,d] (later matchers have depth > d and only touch levels
	// > d), so those levels are finalized and we can short-circuit if any
	// of them is an excluded ancestor.
	finalized := -1
	var isMatch bool
	t.tree.WalkPath(pth, radix.WalkFn[Matcher](func(base string, m Matcher) (radix.WalkFlag, Matcher, error) {
		// The empty base holds global patterns: rel is pth with its leading
		// "/" trimmed and depth is -1 so we don't mark any level finalized;
		// the in-tree root matcher (at "/") still has to run after this one.
		var rel string
		var depth int
		if base == "" {
			rel = pth[1:]
			depth = -1
		} else {
			rel = pth[len(base):]
			depth = strings.Count(base, "/") - 1
		}

		// Iterate the components of rel, applying patterns at each level.
		// We slice rel directly rather than splitting to avoid allocating
		// per-component strings.
		cursor := 0
		level := max(depth, 0)
		for {
			slash := strings.IndexByte(rel[cursor:], '/')
			var sub string
			var elemIsDir bool
			if slash < 0 {
				sub = rel
				elemIsDir = isDir
			} else {
				end := cursor + slash
				sub = rel[:end]
				elemIsDir = true
				cursor = end + 1
			}
			ignored[level] = m.apply(sub, elemIsDir, ignored[level])
			// Level == depth is fully resolved as soon as this matcher
			// finishes with it: later matchers have strictly greater base
			// depth and so cannot touch it. If that level is an ancestor
			// and excluded, pth is ignored regardless of what the deeper
			// levels look like.
			if level == depth && depth < n-1 && ignored[depth] {
				isMatch = true
				return radix.WalkStop, m, nil
			}
			level++
			if slash < 0 {
				break
			}
		}

		upTo := min(depth, n-2)
		for i := finalized + 1; i <= upTo; i++ {
			if ignored[i] {
				isMatch = true
				return radix.WalkStop, m, nil
			}
		}
		finalized = upTo

		return radix.WalkContinue, m, nil
	}))

	if isMatch {
		return true
	}

	for i := finalized + 1; i < n-1; i++ {
		if ignored[i] {
			return true
		}
	}
	return ignored[n-1]
}

// Matcher holds the compiled patterns from a single .gitignore file.
type Matcher struct {
	patterns []pattern
}

// Match reports whether pth (relative to the matcher's base directory) is
// ignored according to this matcher's patterns. isDir reports whether pth is
// a directory. Patterns are evaluated in order; the last matching pattern
// wins, so a later "!negation" can re-include a previously matched path.
func (m Matcher) Match(pth string, isDir bool) bool {
	return m.apply(pth, isDir, false)
}

// apply runs every pattern against pth, returning the resulting ignored
// state given a starting state. Later matches override earlier ones, so
// negations correctly re-include paths matched by earlier patterns.
func (m Matcher) apply(pth string, isDir, current bool) bool {
	for i := range m.patterns {
		p := &m.patterns[i]
		if p.dirOnly && !isDir {
			continue
		}
		if p.match(pth) {
			current = !p.negate
		}
	}
	return current
}

type pattern struct {
	raw     string
	globs   []glob.Glob
	negate  bool
	dirOnly bool
}

func (p *pattern) match(s string) bool {
	for _, g := range p.globs {
		if g.Match(s) {
			return true
		}
	}
	return false
}

// ParseIgnoreFile parses .gitignore file content into a Matcher. Blank lines
// and lines beginning with "#" are skipped. Trailing whitespace on each line
// is stripped.
func ParseIgnoreFile(content string) Matcher {
	var lines []string
	for line := range strings.SplitSeq(content, "\n") {
		line = strings.TrimSuffix(line, "\r")
		line = strings.TrimRight(line, " \t")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
	}
	return parsePatternList(lines)
}

func parsePatternList(lines []string) Matcher {
	var m Matcher
	for _, line := range lines {
		if line == "" {
			continue
		}
		if p, ok := parsePattern(line); ok {
			m.patterns = append(m.patterns, p)
		}
	}
	return m
}

func parsePattern(line string) (pattern, bool) {
	pat := pattern{raw: line}

	switch {
	case strings.HasPrefix(line, `\!`), strings.HasPrefix(line, `\#`):
		line = line[1:]
	case strings.HasPrefix(line, "!"):
		pat.negate = true
		line = line[1:]
	}

	if strings.HasSuffix(line, "/") {
		pat.dirOnly = true
		line = strings.TrimSuffix(line, "/")
	}
	if line == "" {
		return pattern{}, false
	}

	// A pattern with no slash (other than the trailing one we stripped) matches
	// at any depth; otherwise it is anchored to the matcher's base directory.
	anchored := strings.Contains(line, "/")
	line = strings.TrimPrefix(line, "/")
	if line == "" {
		return pattern{}, false
	}

	var globs []string
	switch {
	case !anchored:
		// Match either at the base or at any sub-depth. gobwas/glob's "**/x"
		// does not match bare "x", so we add the literal alternative too.
		globs = []string{line, "**/" + line}
	case strings.HasPrefix(line, "**/"):
		// "**/x" should also match "x" at the base per gitignore semantics.
		globs = []string{line, line[3:]}
	default:
		globs = []string{line}
	}

	for _, g := range globs {
		c, err := glob.Compile(g, '/')
		if err != nil {
			return pattern{}, false
		}
		pat.globs = append(pat.globs, c)
	}
	return pat, true
}

func normalizeBase(p string) string {
	if p == "" {
		// Reserved for global patterns; "" is a prefix of every path so
		// WalkPath visits it first, ahead of any in-tree matcher.
		return ""
	}
	if p == "/" || p == "." {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p
}

func normalizePath(p string) string {
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if p != "/" {
		p = strings.TrimSuffix(p, "/")
	}
	return p
}
