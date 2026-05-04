// Copyright 2026 Bjørn Erik Pedersen
// SPDX-License-Identifier: MIT

package gogitignore

import (
	"os"
	"os/exec"
	"path"
	"strings"
	"testing"

	qt "github.com/frankban/quicktest"
	"github.com/rogpeppe/go-internal/txtar"
)

type pathToCheck struct {
	path  string
	isDir bool
}

func TestTree(t *testing.T) {
	cases := []struct {
		name  string
		files string
		check []pathToCheck
	}{
		{
			name: "directory_pattern",
			files: `
-- bar.txt --
B
-- foo/baz.txt --
B
-- .gitignore --
foo/
`,
			check: []pathToCheck{
				{"/bar.txt", false},
				{"/foo", true},
				{"/foo/baz.txt", false},
				{"/foo/missing.txt", false},
			},
		},
		{
			name: "unanchored_filename",
			files: `
-- bar.txt --
B
-- sub/bar.txt --
B
-- sub/deep/bar.txt --
B
-- baz.txt --
B
-- .gitignore --
bar.txt
`,
			check: []pathToCheck{
				{"/bar.txt", false},
				{"/sub/bar.txt", false},
				{"/sub/deep/bar.txt", false},
				{"/baz.txt", false},
			},
		},
		{
			name: "anchored_filename",
			files: `
-- bar.txt --
B
-- sub/bar.txt --
B
-- .gitignore --
/bar.txt
`,
			check: []pathToCheck{
				{"/bar.txt", false},
				{"/sub/bar.txt", false},
			},
		},
		{
			name: "wildcard_extension",
			files: `
-- a.log --
A
-- b.txt --
B
-- sub/c.log --
C
-- .gitignore --
*.log
`,
			check: []pathToCheck{
				{"/a.log", false},
				{"/b.txt", false},
				{"/sub/c.log", false},
			},
		},
		{
			name: "negation",
			files: `
-- a.log --
A
-- important.log --
I
-- .gitignore --
*.log
!important.log
`,
			check: []pathToCheck{
				{"/a.log", false},
				{"/important.log", false},
			},
		},
		{
			name: "negation_blocked_by_dir",
			files: `
-- foo/keep.txt --
K
-- foo/other.txt --
O
-- .gitignore --
foo/
!foo/keep.txt
`,
			check: []pathToCheck{
				{"/foo", true},
				{"/foo/keep.txt", false},
				{"/foo/other.txt", false},
			},
		},
		{
			name: "double_star_prefix",
			files: `
-- foo --
F
-- a/foo --
F
-- a/b/foo --
F
-- a/b/bar --
B
-- .gitignore --
**/foo
`,
			check: []pathToCheck{
				{"/foo", false},
				{"/a/foo", false},
				{"/a/b/foo", false},
				{"/a/b/bar", false},
			},
		},
		{
			name: "double_star_suffix",
			files: `
-- foo/a --
A
-- foo/sub/b --
B
-- bar --
B
-- .gitignore --
foo/**
`,
			check: []pathToCheck{
				{"/foo", true},
				{"/foo/a", false},
				{"/foo/sub/b", false},
				{"/bar", false},
			},
		},
		{
			name: "double_star_middle",
			files: `
-- a/b --
B
-- a/x/b --
B
-- a/x/y/b --
B
-- a/c --
C
-- .gitignore --
a/**/b
`,
			check: []pathToCheck{
				{"/a/b", false},
				{"/a/x/b", false},
				{"/a/x/y/b", false},
				{"/a/c", false},
			},
		},
		{
			name: "anchored_subpath",
			files: `
-- foo/bar --
B
-- sub/foo/bar --
B
-- .gitignore --
foo/bar
`,
			check: []pathToCheck{
				{"/foo/bar", false},
				{"/sub/foo/bar", false},
			},
		},
		{
			name: "nested_gitignore",
			files: `
-- bar.txt --
B
-- sub/bar.txt --
B
-- sub/baz.txt --
B
-- sub/deep/bar.txt --
B
-- .gitignore --
# nothing here
-- sub/.gitignore --
bar.txt
`,
			check: []pathToCheck{
				{"/bar.txt", false},
				{"/sub/bar.txt", false},
				{"/sub/baz.txt", false},
				{"/sub/deep/bar.txt", false},
			},
		},
		{
			name: "nested_gitignore_negation",
			files: `
-- a.log --
A
-- sub/a.log --
A
-- sub/b.log --
B
-- .gitignore --
*.log
-- sub/.gitignore --
!a.log
`,
			check: []pathToCheck{
				{"/a.log", false},
				{"/sub/a.log", false},
				{"/sub/b.log", false},
			},
		},
		{
			name: "comments_and_blanks",
			files: `
-- foo --
F
-- bar --
B
-- baz --
B
-- .gitignore --
# comment line
foo

bar
`,
			check: []pathToCheck{
				{"/foo", false},
				{"/bar", false},
				{"/baz", false},
			},
		},
		{
			name: "escaped_special_prefix",
			files: `
-- !important --
I
-- #hash --
H
-- .gitignore --
\!important
\#hash
`,
			check: []pathToCheck{
				{`/!important`, false},
				{`/#hash`, false},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := qt.New(t)
			h := &testHelper{C: c}
			tree := New()
			h.writeFiles(tree, tc.files)
			for _, p := range tc.check {
				expected := h.shouldIgnoreGit(p)
				actual := tree.Match(p.path, p.isDir)
				c.Assert(actual, qt.Equals, expected,
					qt.Commentf("path=%s isDir=%v git=%v ours=%v", p.path, p.isDir, expected, actual))
			}
		})
	}
}

func TestParseIgnoreFile(t *testing.T) {
	c := qt.New(t)

	m := ParseIgnoreFile("# comment\n\n  \nfoo\n!bar\nbaz/\n")
	c.Assert(m.patterns, qt.HasLen, 3)

	c.Assert(m.Match("foo", false), qt.IsTrue)
	c.Assert(m.Match("sub/foo", false), qt.IsTrue)
	c.Assert(m.Match("baz", true), qt.IsTrue)
	c.Assert(m.Match("baz", false), qt.IsFalse) // dirOnly
	c.Assert(m.Match("bar", false), qt.IsFalse) // !bar negates nothing here
	c.Assert(m.Match("other", false), qt.IsFalse)
}

// TestTreeGlobalPatterns covers the reserved empty path "" — patterns added
// there are evaluated first and can be overridden by an in-tree .gitignore,
// matching how Git layers core.excludesFile underneath the working tree's
// .gitignore files.
func TestTreeGlobalPatterns(t *testing.T) {
	c := qt.New(t)

	tree := New()
	tree.AddPatterns("", "*.log", "build/")

	c.Assert(tree.Match("/a.log", false), qt.IsTrue, qt.Commentf("global *.log applies at root"))
	c.Assert(tree.Match("/sub/a.log", false), qt.IsTrue, qt.Commentf("global *.log applies at any depth"))
	c.Assert(tree.Match("/build", true), qt.IsTrue, qt.Commentf("global build/ applies as dir"))
	c.Assert(tree.Match("/build/x.txt", false), qt.IsTrue, qt.Commentf("excluded ancestor wins"))
	c.Assert(tree.Match("/main.go", false), qt.IsFalse)

	// The in-tree root .gitignore runs *after* the global, so a negation
	// there must be able to re-include a path the global excluded.
	tree.AddPatterns("/", "!important.log")
	c.Assert(tree.Match("/important.log", false), qt.IsFalse, qt.Commentf("root .gitignore overrides global"))
	c.Assert(tree.Match("/a.log", false), qt.IsTrue, qt.Commentf("non-negated globals still apply"))

	// Adding at "" must not collide with adding at "/": they're two distinct
	// matchers in the tree, both applicable to every path.
	tree.AddPatterns("", "*.tmp")
	c.Assert(tree.Match("/x.tmp", false), qt.IsTrue)
	c.Assert(tree.Match("/important.log", false), qt.IsFalse, qt.Commentf("root negation still wins"))
	c.Assert(tree.Match("/a.log", false), qt.IsFalse, qt.Commentf("global was replaced; *.log no longer there"))
}

func TestAddPatternsReplaces(t *testing.T) {
	c := qt.New(t)
	tree := New()
	tree.AddPatterns("/", "*.log")
	c.Assert(tree.Match("/a.log", false), qt.IsTrue)
	tree.AddPatterns("/", "*.tmp")
	c.Assert(tree.Match("/a.log", false), qt.IsFalse)
	c.Assert(tree.Match("/a.tmp", false), qt.IsTrue)
}

type testHelper struct {
	*qt.C
	root string
}

func (t *testHelper) writeFiles(tree *Tree, files string) {
	t.Helper()
	t.root = t.TempDir()
	t.initGitRepo()
	data := txtar.Parse([]byte(files))

	for _, f := range data.Files {
		pth := t.root + "/" + f.Name
		if err := t.writeFile(pth, f.Data); err != nil {
			t.Fatalf("failed to write file %q: %v", pth, err)
		}
		if path.Base(f.Name) == ".gitignore" {
			m := ParseIgnoreFile(string(f.Data))
			dir := path.Dir(f.Name)
			if dir == "." {
				dir = "/"
			} else {
				dir = "/" + dir
			}
			tree.AddMatcher(dir, m)
		}
	}
}

func (t *testHelper) writeFile(pth string, data []byte) error {
	t.Helper()
	if err := os.MkdirAll(path.Dir(pth), 0o755); err != nil {
		return err
	}
	return os.WriteFile(pth, data, 0o644)
}

func (t *testHelper) initGitRepo() {
	t.Helper()
	for _, args := range [][]string{
		{"git", "init", "--initial-branch=main"},
		{"git", "config", "user.email", "test@example.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = t.root
		if err := cmd.Run(); err != nil {
			t.Fatalf("git init: %v", err)
		}
	}
}

// shouldIgnoreGit asks `git check-ignore` whether p would be ignored, used
// as the oracle the implementation is compared against.
func (t *testHelper) shouldIgnoreGit(p pathToCheck) bool {
	t.Helper()
	rel := strings.TrimPrefix(p.path, "/")
	cmd := exec.Command("git", "check-ignore", "-q", rel)
	cmd.Dir = t.root
	err := cmd.Run()
	if err == nil {
		return true
	}
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
		return false
	}
	t.Fatalf("git check-ignore %q: %v", rel, err)
	return false
}
