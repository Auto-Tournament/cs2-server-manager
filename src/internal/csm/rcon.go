package csm

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Minimal Source RCON client (the TCP protocol CS2 speaks on its game port).
// csm uses it to ask a running server whether it is idle before an
// automatic update: `status` for connected players and the plugin's
// `get5_status` for the match state.

const (
	rconTypeAuth         = 3
	rconTypeAuthResponse = 2
	rconTypeExec         = 2
	rconTypeResponse     = 0

	rconMaxPacket = 4096 + 14
)

// ErrRCONAuth means the server rejected the RCON password.
var ErrRCONAuth = errors.New("rcon: authentication failed")

type rconConn struct {
	conn    net.Conn
	timeout time.Duration
	nextID  int32
}

func rconWritePacket(w io.Writer, id, typ int32, body string) error {
	var buf bytes.Buffer
	size := int32(4 + 4 + len(body) + 2)
	_ = binary.Write(&buf, binary.LittleEndian, size)
	_ = binary.Write(&buf, binary.LittleEndian, id)
	_ = binary.Write(&buf, binary.LittleEndian, typ)
	buf.WriteString(body)
	buf.Write([]byte{0, 0})
	_, err := w.Write(buf.Bytes())
	return err
}

func rconReadPacket(r io.Reader) (id, typ int32, body string, err error) {
	var size int32
	if err = binary.Read(r, binary.LittleEndian, &size); err != nil {
		return 0, 0, "", err
	}
	if size < 10 || size > rconMaxPacket {
		return 0, 0, "", fmt.Errorf("rcon: bad packet size %d", size)
	}
	payload := make([]byte, size)
	if _, err = io.ReadFull(r, payload); err != nil {
		return 0, 0, "", err
	}
	id = int32(binary.LittleEndian.Uint32(payload[0:4]))
	typ = int32(binary.LittleEndian.Uint32(payload[4:8]))
	body = strings.TrimRight(string(payload[8:]), "\x00")
	return id, typ, body, nil
}

// rconDial connects to addr and authenticates with password.
func rconDial(addr, password string, timeout time.Duration) (*rconConn, error) {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	c := &rconConn{conn: conn, timeout: timeout, nextID: 1}
	if err := c.auth(password); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return c, nil
}

func (c *rconConn) Close() error { return c.conn.Close() }

func (c *rconConn) auth(password string) error {
	_ = c.conn.SetDeadline(time.Now().Add(c.timeout))
	id := c.nextID
	c.nextID++
	if err := rconWritePacket(c.conn, id, rconTypeAuth, password); err != nil {
		return err
	}
	// Servers send an empty RESPONSE_VALUE before the AUTH_RESPONSE; skip it.
	for {
		rid, typ, _, err := rconReadPacket(c.conn)
		if err != nil {
			return err
		}
		if typ != rconTypeAuthResponse {
			continue
		}
		if rid == -1 || rid != id {
			return ErrRCONAuth
		}
		return nil
	}
}

// Exec runs cmd and returns its console output. CS2 may split long output
// over several packets, so after the first one it keeps reading until the
// server goes quiet for a moment.
func (c *rconConn) Exec(cmd string) (string, error) {
	id := c.nextID
	c.nextID++
	_ = c.conn.SetDeadline(time.Now().Add(c.timeout))
	if err := rconWritePacket(c.conn, id, rconTypeExec, cmd); err != nil {
		return "", err
	}
	var out strings.Builder
	got := false
	for {
		rid, typ, body, err := rconReadPacket(c.conn)
		if err != nil {
			var ne net.Error
			if got && errors.As(err, &ne) && ne.Timeout() {
				break
			}
			if got && errors.Is(err, io.EOF) {
				break
			}
			return out.String(), err
		}
		if typ != rconTypeResponse || rid != id {
			continue
		}
		out.WriteString(body)
		got = true
		// Wait briefly for continuation packets.
		_ = c.conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	}
	return out.String(), nil
}

// serverRCONPassword returns the RCON password server-N uses: its own
// server.cfg first (the game reads that one), then the shared cs2-config
// server.cfg.
func serverRCONPassword(user string, server int) string {
	cfg := filepath.Join("/home", user, fmt.Sprintf("server-%d", server), "game", "csgo", "cfg", "server.cfg")
	if data, err := os.ReadFile(cfg); err == nil {
		if p := parseRCONPassword(string(data)); p != "" {
			return p
		}
	}
	return detectRCONPassword(user)
}
