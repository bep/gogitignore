// Copyright 2026 Bjørn Erik Pedersen
// SPDX-License-Identifier: MIT

package gogitignore

import (
	"fmt"
	"testing"
)

// realisticGitignore is a reasonably typical .gitignore covering several
// pattern shapes: literal, wildcard, anchored, dir-only, **/, /**, and negation.
const realisticGitignore = `
# Build output
bin/
build/
dist/

# Logs
*.log
logs/

# OS junk
.DS_Store
Thumbs.db

# Editor
*.swp
*.swo
.vscode/
.idea/

# Language
node_modules/
__pycache__/
*.pyc

# Anchored
/private
/secrets.env

# Negation
!important.log

# Globstar
**/generated
src/**/*.tmp
`

func BenchmarkParseIgnoreFile(b *testing.B) {
	for b.Loop() {
		_ = mustParse(realisticGitignore)
	}
}

func BenchmarkMatchRootShallow(b *testing.B) {
	tree := New()
	tree.InsertMatcher("/", mustParse(realisticGitignore))

	b.Run("ignored_literal", func(b *testing.B) {
		for b.Loop() {
			tree.Match("/bin/app", false)
		}
	})
	b.Run("ignored_wildcard", func(b *testing.B) {
		for b.Loop() {
			tree.Match("/server.log", false)
		}
	})
	b.Run("not_ignored", func(b *testing.B) {
		for b.Loop() {
			tree.Match("/main.go", false)
		}
	})
}

func BenchmarkMatchRootDeep(b *testing.B) {
	tree := New()
	tree.InsertMatcher("/", mustParse(realisticGitignore))

	b.Run("ignored_via_dir", func(b *testing.B) {
		// /node_modules is dir-ignored at root; descendant lookup walks ancestors.
		for b.Loop() {
			tree.Match("/node_modules/lib/sub/pkg/index.js", false)
		}
	})
	b.Run("ignored_via_globstar", func(b *testing.B) {
		for b.Loop() {
			tree.Match("/src/a/b/c/d/foo.tmp", false)
		}
	})
	b.Run("not_ignored_deep", func(b *testing.B) {
		for b.Loop() {
			tree.Match("/src/a/b/c/d/main.go", false)
		}
	})
}

func BenchmarkMatchNestedMatchers(b *testing.B) {
	// Build a tree with .gitignore matchers at increasing depths so each
	// Match call has to consult and combine multiple matchers.
	tree := New()
	tree.InsertMatcher("/", mustParse("*.log\n"))
	depth := 8
	base := ""
	for i := range depth {
		base += fmt.Sprintf("/d%d", i)
		tree.InsertMatcher(base, mustParse(fmt.Sprintf("local%d.tmp\n", i)))
	}
	innerHit := base + "/local7.tmp"
	innerMiss := base + "/main.go"

	b.Run("hit_inner", func(b *testing.B) {
		for b.Loop() {
			tree.Match(innerHit, false)
		}
	})
	b.Run("miss_inner", func(b *testing.B) {
		for b.Loop() {
			tree.Match(innerMiss, false)
		}
	})
}

func BenchmarkMatcherDirect(b *testing.B) {
	m := mustParse(realisticGitignore)
	b.Run("hit", func(b *testing.B) {
		for b.Loop() {
			m.Match("server.log", false)
		}
	})
	b.Run("miss", func(b *testing.B) {
		for b.Loop() {
			m.Match("main.go", false)
		}
	})
}
