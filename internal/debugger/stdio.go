package debugger

import (
	"io"
	"net"
	"time"
)

// stdioConn adapts a process's stdin/stdout to net.Conn for the DAP protocol.
type stdioConn struct {
	io.Reader
	io.Writer
	closer func() error
}

func (c *stdioConn) Close() error                       { return c.closer() }
func (c *stdioConn) LocalAddr() net.Addr                { return addr{} }
func (c *stdioConn) RemoteAddr() net.Addr               { return addr{} }
func (c *stdioConn) SetDeadline(t time.Time) error      { return nil }
func (c *stdioConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *stdioConn) SetWriteDeadline(t time.Time) error { return nil }

type addr struct{}

func (addr) Network() string { return "stdio" }
func (addr) String() string  { return "stdio" }
