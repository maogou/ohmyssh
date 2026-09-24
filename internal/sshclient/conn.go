package sshclient

import (
	"io"
	"net"
	"os/exec"
	"sync"
	"time"
)

// cmdConn adapts a ProxyCommand's stdio to net.Conn so the SSH handshake can
// run over it. Deadlines are accepted and ignored, and are here only to satisfy
// net.Conn: a pipe has no deadline support, and nothing sets one on this
// connection — a handshake that has to be bounded is ended by closing it.
type cmdConn struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	closeOnce sync.Once
	closeErr  error
}

func (c *cmdConn) Read(p []byte) (int, error)  { return c.stdout.Read(p) }
func (c *cmdConn) Write(p []byte) (int, error) { return c.stdin.Write(p) }

// proxyExitGrace is how long a proxy command is given to exit on its own after
// its pipes have been closed, before it is killed.
const proxyExitGrace = 2 * time.Second

func (c *cmdConn) Close() error {
	c.closeOnce.Do(func() {
		// Close stdin first so the proxy sees EOF and can exit on its own.
		_ = c.stdin.Close()
		_ = c.stdout.Close()

		// Closing the pipes is a request, not an instruction: a proxy that is
		// blocked on a socket of its own reads nothing and sees nothing, and
		// waiting for it to notice would hold this call — and whatever ended in
		// it, a timeout, a ctrl+c, or the user quitting a finished session — open
		// for as long as the process cares to live. It is given a moment to leave
		// in an orderly way, and then killed.
		//
		// Wait is reaped either way, so the process cannot be left a zombie and
		// the goroutine below cannot outlive this call.
		exited := make(chan error, 1)
		go func() { exited <- c.cmd.Wait() }()
		select {
		case err := <-exited:
			if err != nil {
				c.closeErr = err
			}
		case <-time.After(proxyExitGrace):
			_ = c.cmd.Process.Kill()
			<-exited
		}
	})
	return c.closeErr
}

type proxyAddr struct{ name string }

func (a proxyAddr) Network() string { return "proxy" }
func (a proxyAddr) String() string  { return a.name }

func (c *cmdConn) LocalAddr() net.Addr  { return proxyAddr{"proxy-command"} }
func (c *cmdConn) RemoteAddr() net.Addr { return proxyAddr{"proxy-command"} }

func (c *cmdConn) SetDeadline(time.Time) error      { return nil }
func (c *cmdConn) SetReadDeadline(time.Time) error  { return nil }
func (c *cmdConn) SetWriteDeadline(time.Time) error { return nil }
