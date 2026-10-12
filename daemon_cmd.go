package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/sanity-labs/agenda/internal/daemon"
)

// The daemon subcommands talk to the socket and need no config, so they
// run before it loads like the other plain commands. `agenda daemon`
// itself needs the views and is handled in main after they are built.

func runStatus(w io.Writer, _ []string) int {
	sock, err := daemon.SocketPath()
	if err != nil {
		fmt.Fprintln(w, "agenda:", err)
		return 1
	}
	fmt.Fprintf(w, "client:\n  version: %s\n  protocol: %d\n\n", versionString(), daemon.Protocol)
	c, err := daemon.Dial(sock)
	if err != nil {
		fmt.Fprintf(w, "daemon:\n  status: not running\n  socket: %s\n", sock)
		return 1
	}
	defer c.Close()
	var st daemon.Status
	if err := c.Call(daemon.MethodStatus, nil, &st); err != nil {
		fmt.Fprintf(w, "daemon:\n  status: unreachable (%v)\n  socket: %s\n", err, sock)
		return 1
	}
	compatible := "yes"
	if st.Protocol != daemon.Protocol {
		compatible = fmt.Sprintf("no (daemon speaks %d; run agenda restart)", st.Protocol)
	}
	stale := "no"
	if st.Version != versionString() {
		stale = "yes (daemon is " + st.Version + "; run agenda restart)"
	}
	fmt.Fprintf(w, "daemon:\n  status: running\n  version: %s\n  pid: %d\n  up: %s\n  views: %v\n  clients: %d\n  socket: %s\n  protocol_compatible: %s\n  binary_stale: %s\n",
		st.Version, st.PID, time.Since(st.Started).Round(time.Second), st.Views, st.Clients, sock, compatible, stale)
	return 0
}

func runStop(w io.Writer, _ []string) int {
	sock, err := daemon.SocketPath()
	if err != nil {
		fmt.Fprintln(w, "agenda:", err)
		return 1
	}
	c, err := daemon.Dial(sock)
	if err != nil {
		fmt.Fprintln(w, "agenda: no daemon running")
		return 1
	}
	defer c.Close()
	if err := c.Call(daemon.MethodStop, nil, nil); err != nil {
		fmt.Fprintln(w, "agenda: stop failed:", err)
		return 1
	}
	// Wait for the socket to go, so a restart right after does not race.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if probe, err := daemon.Dial(sock); err != nil {
			fmt.Fprintln(w, "daemon stopped")
			return 0
		} else {
			probe.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}
	fmt.Fprintln(w, "daemon acknowledged the stop but is still listening")
	return 1
}

func runRestart(w io.Writer, args []string) int {
	sock, _ := daemon.SocketPath()
	if _, err := daemon.Dial(sock); err == nil {
		if code := runStop(w, args); code != 0 {
			return code
		}
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(w, "agenda:", err)
		return 1
	}
	c, err := daemon.Spawn(exe)
	if err != nil {
		fmt.Fprintln(w, "agenda:", err)
		return 1
	}
	c.Close()
	return runStatus(w, nil)
}
