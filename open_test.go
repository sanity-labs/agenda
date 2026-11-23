package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/sanity-labs/agenda/internal/tui"
	"github.com/sanity-labs/agenda/internal/ui"
)

func TestParseOpen(t *testing.T) {
	pr := func(url string) *tui.Open { return &tui.Open{Ref: ui.Ref{Kind: "pr", ID: url, URL: url}} }
	cases := []struct {
		view string
		args []string
		want *tui.Open
		err  string
	}{
		{"linear", nil, nil, ""},
		{"linear", []string{"sre-1234"}, &tui.Open{Ref: ui.Ref{Kind: "linear", ID: "SRE-1234"}}, ""},
		{"linear", []string{"nope"}, nil, "issue id"},
		{"linear", []string{"SRE-1", "SRE-2"}, nil, "issue id"},
		{"prs", nil, nil, ""},
		{"prs", []string{"--repo", "agenda", "--pr", "88"}, pr("https://github.com/sanity-io/agenda/pull/88"), ""},
		{"prs", []string{"--repo=sanity-labs/agenda", "--pr=88"}, pr("https://github.com/sanity-labs/agenda/pull/88"), ""},
		{"prs", []string{"--repo", "agenda"}, &tui.Open{Repo: "sanity-io/agenda"}, ""},
		{"prs", []string{"--pr", "8"}, nil, "needs --repo"},
		{"prs", []string{"--repo", "agenda", "--pr", "x"}, nil, "bad --pr"},
		{"prs", []string{"--repo", "agenda", "--pr", "0"}, nil, "bad --pr"},
		{"prs", []string{"--repo", "a/b/c"}, nil, "bad --repo"},
		{"prs", []string{"--repo"}, nil, "needs an argument"},
		{"prs", []string{"--nope"}, nil, "not defined"},
		{"prs", []string{"--pr", "5", "--repo", "x"}, pr("https://github.com/sanity-io/x/pull/5"), ""},
		{"prs", []string{"stray"}, nil, "unexpected"},
		{"sessions", []string{"whatever"}, nil, ""},
	}
	for _, c := range cases {
		cmd, _ := lookup(c.view)
		got, _, err := parseArgs(cmd, c.args)
		if (err != nil) != (c.err != "") || err != nil && !strings.Contains(err.Error(), c.err) {
			t.Errorf("%s %v: err = %v, want %q", c.view, c.args, err, c.err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %v: got %+v, want %+v", c.view, c.args, got, c.want)
		}
	}
}
