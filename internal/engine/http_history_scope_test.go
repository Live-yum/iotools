package engine

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestHistoryRetentionTokenBindsDatabaseAndContents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.sqlite")
	content := strings.Repeat("a", 64)
	token, err := scopeHTTPHistoryRetentionToken(path, content)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range [][2]string{
		{filepath.Join(filepath.Dir(path), "other.sqlite"), content},
		{path, strings.Repeat("b", 64)},
	} {
		other, err := scopeHTTPHistoryRetentionToken(change[0], change[1])
		if err != nil || other == token {
			t.Fatalf("changed scope reused consent: %q %v", other, err)
		}
	}
}

func TestHistoryRetentionTokenNormalizesLexicalAliases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.sqlite")
	alias := filepath.Dir(path) + string(filepath.Separator) + "." + string(filepath.Separator) + filepath.Base(path)
	content := strings.Repeat("a", 64)
	first, err := scopeHTTPHistoryRetentionToken(path, content)
	if err != nil {
		t.Fatal(err)
	}
	second, err := scopeHTTPHistoryRetentionToken(alias, content)
	if err != nil || first != second {
		t.Fatalf("lexical aliases differ: %q %q %v", first, second, err)
	}
}

func TestHistoryRetentionTokenRejectsMissingScopeOrIncompleteDigest(t *testing.T) {
	for _, input := range [][2]string{
		{"", strings.Repeat("a", 64)},
		{"history\x00.sqlite", strings.Repeat("a", 64)},
		{"history.sqlite", ""},
		{"history.sqlite", "aa"},
		{"history.sqlite", strings.Repeat("z", 64)},
	} {
		if _, err := scopeHTTPHistoryRetentionToken(input[0], input[1]); err == nil {
			t.Fatalf("invalid scope accepted: %q", input)
		}
	}
}
