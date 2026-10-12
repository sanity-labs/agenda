package daemon

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// Spawn starts `exe daemon` detached from this terminal, its output in
// the log file, and waits for the socket to answer. The daemon loads its
// own config; nothing is passed but the binary.
func Spawn(exe string) (*Client, error) {
	sock, err := SocketPath()
	if err != nil {
		return nil, err
	}
	logPath, err := LogPath()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return nil, err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	defer logFile.Close()

	cmd := exec.Command(exe, "daemon")
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.Stdin = nil
	// Its own session: closing the terminal that spawned it must not take
	// it down, and a ctrl+c here must not reach it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	_ = cmd.Process.Release()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := Dial(sock); err == nil {
			return c, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, errors.New("daemon did not come up; see " + logPath)
}

// Connect dials the daemon, spawning one when none answers.
func Connect(exe string) (*Client, error) {
	sock, err := SocketPath()
	if err != nil {
		return nil, err
	}
	if c, err := Dial(sock); err == nil {
		return c, nil
	}
	return Spawn(exe)
}
