package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/sanity-labs/agenda/internal/tui"
	"github.com/sanity-labs/agenda/internal/ui"
)

func TestParseOpen(t *testing.T) {
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
		{"prs", []string{"--repo", "agenda", "--pr", "88"}, &tui.Open{Repo: "agenda", Ref: ui.Ref{Kind: "pr", ID: "88"}}, ""},
		{"prs", []string{"--repo=sanity-labs/agenda", "--pr=88"}, &tui.Open{Repo: "sanity-labs/agenda", Ref: ui.Ref{Kind: "pr", ID: "88"}}, ""},
		{"prs", []string{"--repo", "agenda"}, &tui.Open{Repo: "agenda"}, ""},
		{"prs", []string{"--pr", "8"}, nil, "needs --repo"},
		{"prs", []string{"--repo", "agenda", "--pr", "x"}, nil, "bad --pr"},
		{"prs", []string{"--repo", "agenda", "--pr", "0"}, nil, "bad --pr"},
		{"prs", []string{"--repo", "a/b/c"}, nil, "bad --repo"},
		{"prs", []string{"--repo"}, nil, "needs an argument"},
		{"prs", []string{"--nope"}, nil, "not defined"},
		{"prs", []string{"--pr", "5", "--repo", "x"}, &tui.Open{Repo: "x", Ref: ui.Ref{Kind: "pr", ID: "5"}}, ""},
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

func TestResolveOpenDefaultOrg(t *testing.T) {
	run := func(org string, args ...string) (*tui.Open, error) {
		cmd, _ := lookup("prs")
		o, _, err := parseArgs(cmd, args)
		if err != nil {
			t.Fatal(err)
		}
		return o, resolveOpen(o, org)
	}
	o, err := run("acme", "--repo", "agenda")
	if err != nil || o.Repo != "acme/agenda" {
		t.Errorf("bare with default_org: %+v, %v", o, err)
	}
	o, err = run("acme", "--repo", "agenda", "--pr", "3")
	if err != nil || o.Ref.URL != "https://github.com/acme/agenda/pull/3" || o.Repo != "" {
		t.Errorf("bare PR with default_org: %+v, %v", o, err)
	}
	if _, err = run("", "--repo", "agenda"); err == nil || !strings.Contains(err.Error(), "github.default_org") {
		t.Errorf("bare without default_org: err = %v, want one naming github.default_org", err)
	}
	o, err = run("acme", "--repo", "other/agenda", "--pr", "3")
	if err != nil || o.Ref.URL != "https://github.com/other/agenda/pull/3" {
		t.Errorf("org/repo must ignore default_org: %+v, %v", o, err)
	}
	if o, err = run("", "--repo", "other/agenda"); err != nil || o.Repo != "other/agenda" {
		t.Errorf("org/repo with no default_org: %+v, %v", o, err)
	}
}

func TestDaemonArgIsCaseInsensitive(t *testing.T) {
	for _, a := range []string{"daemon", "DAEMON", "Daemon"} {
		if !isDaemonArg(a) {
			t.Errorf("%q not treated as daemon", a)
		}
	}
}
