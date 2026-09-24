package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/sanity-labs/agenda/internal/update"
)

func runUpdate(w io.Writer, _ []string) int {
	ctx, cancel := context.WithTimeout(context.Background(), update.Timeout)
	defer cancel()

	res := update.Check(ctx, versionString())
	switch {
	case res.Err != nil:
		fmt.Fprintln(os.Stderr, "agenda: update check failed:", res.Err)
		return 1
	case res.Available:
		fmt.Fprintf(w, "agenda %s is available (you have %s)\n", res.Latest.Version, res.Current)
		fmt.Fprintf(w, "  %s\n", update.HowToUpdate())
		fmt.Fprintf(w, "  %s\n", res.Latest.URL)
	default:
		fmt.Fprintf(w, "agenda %s is up to date\n", res.Current)
	}
	return 0
}
