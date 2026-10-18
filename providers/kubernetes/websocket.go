package kubernetes

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

// A minimal client side of RFC 6455, just enough to carry the Kubernetes
// API's streaming subresources (port-forward) without a dependency. Only
// what the API server sends is handled: binary and text messages,
// fragmentation, ping/pong and close. Extensions are never negotiated.

const (
	// maxMessage bounds one reassembled message from the server, so a
	// misbehaving peer can't make us buffer without limit.
	maxMessage = 1 << 20

	opContinuation = 0x0
	opText         = 0x1
	opBinary       = 0x2
	opClose        = 0x8
	opPing         = 0x9
	opPong         = 0xa

	wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
)

var errMessageTooLarge = errors.New("kubernetes: websocket: message too large")

// wsConn is an established WebSocket connection to the API server.
type wsConn struct {
	conn net.Conn
	br   *bufio.Reader

	wmu  sync.Mutex // serialises frame writes (data, pong, close)
	werr error      // the first write failure; every later write fails too

	closeOnce sync.Once
	closeErr  error
}

// dialWebSocket opens a WebSocket to path on the API server, offering one
// subprotocol, and returns once the server has accepted it. The upgrade
// request carries the Client's own credentials: its TLS client certificate
// or bearer token. ctx bounds the dial and the handshake only.
func (c *Client) dialWebSocket(ctx context.Context, path, protocol string) (*wsConn, error) {
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: websocket: base URL: %w", err)
	}
	host := base.Host
	if base.Port() == "" {
		if base.Scheme == "https" {
			host = net.JoinHostPort(base.Hostname(), "443")
		} else {
			host = net.JoinHostPort(base.Hostname(), "80")
		}
	}
	if base.Scheme == "http" && c.token != "" && !loopback(base.Hostname()) {
		return nil, errors.New("kubernetes: websocket: refusing to send a bearer token over plain http to a non-loopback host")
	}
	var conn net.Conn
	switch base.Scheme {
	case "https":
		cfg := &tls.Config{}
		if c.tls != nil {
			cfg = c.tls.Clone()
		}
		// The upgrade is an HTTP/1.1 mechanism; HTTP/2 would refuse it.
		cfg.NextProtos = []string{"http/1.1"}
		if cfg.ServerName == "" {
			cfg.ServerName = base.Hostname()
		}
		conn, err = (&tls.Dialer{Config: cfg}).DialContext(ctx, "tcp", host)
	case "http":
		conn, err = (&net.Dialer{}).DialContext(ctx, "tcp", host)
	default:
		return nil, fmt.Errorf("kubernetes: websocket: unsupported scheme %q", base.Scheme)
	}
	if err != nil {
		return nil, fmt.Errorf("kubernetes: websocket: dial: %w", err)
	}
	ws, err := c.handshake(ctx, conn, base, path, protocol)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return ws, nil
}

func (c *Client) handshake(ctx context.Context, conn net.Conn, base *url.URL, path, protocol string) (ws *wsConn, err error) {
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	// Cancelling ctx aborts a handshake stuck in a write or read, even when
	// ctx has no deadline.
	stop := context.AfterFunc(ctx, func() { conn.SetDeadline(time.Unix(1, 0)) })
	defer func() {
		if !stop() && err == nil {
			// ctx ended just as the handshake finished: the deadline may
			// already be tripped, so the connection can't be trusted.
			ws, err = nil, fmt.Errorf("kubernetes: websocket: %w", ctx.Err())
		}
		if err == nil {
			conn.SetDeadline(time.Time{})
		} else if ctx.Err() != nil {
			err = fmt.Errorf("kubernetes: websocket: %w (%v)", ctx.Err(), err)
		}
	}()
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("kubernetes: websocket: %w", err)
	}
	key := base64.StdEncoding.EncodeToString(nonce)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(base.String(), "/")+path, nil)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: websocket: %w", err)
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", key)
	req.Header.Set("Sec-WebSocket-Protocol", protocol)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if err := req.Write(conn); err != nil {
		return nil, fmt.Errorf("kubernetes: websocket: send upgrade: %w", err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: websocket: read upgrade response: %w", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("kubernetes: websocket: %s: %w", path, ErrNotFound)
		}
		return nil, briefStatus(resp)
	}
	sum := sha1.Sum([]byte(key + wsGUID))
	switch {
	case !strings.EqualFold(resp.Header.Get("Upgrade"), "websocket"):
		return nil, errors.New("kubernetes: websocket: server did not upgrade to websocket")
	case resp.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(sum[:]):
		return nil, errors.New("kubernetes: websocket: bad Sec-WebSocket-Accept")
	case resp.Header.Get("Sec-WebSocket-Protocol") != protocol:
		return nil, fmt.Errorf("kubernetes: websocket: server chose subprotocol %q, want %q", resp.Header.Get("Sec-WebSocket-Protocol"), protocol)
	}
	return &wsConn{conn: conn, br: br}, nil
}

// readMessage returns the next data message (text or binary), answering
// pings along the way. A close frame from the server, or a closed
// connection, yields io.EOF.
func (w *wsConn) readMessage() ([]byte, error) {
	var msg []byte
	started := false
	for {
		var h [2]byte
		if _, err := io.ReadFull(w.br, h[:]); err != nil {
			return nil, eof(err)
		}
		fin, op := h[0]&0x80 != 0, h[0]&0x0f
		if h[0]&0x70 != 0 {
			return nil, errors.New("kubernetes: websocket: reserved bits set")
		}
		if h[1]&0x80 != 0 {
			return nil, errors.New("kubernetes: websocket: masked frame from server")
		}
		n := uint64(h[1] & 0x7f)
		switch n {
		case 126:
			var b [2]byte
			if _, err := io.ReadFull(w.br, b[:]); err != nil {
				return nil, eof(err)
			}
			n = uint64(binary.BigEndian.Uint16(b[:]))
		case 127:
			var b [8]byte
			if _, err := io.ReadFull(w.br, b[:]); err != nil {
				return nil, eof(err)
			}
			n = binary.BigEndian.Uint64(b[:])
		}
		if op >= opClose { // control frame
			if !fin || n > 125 {
				return nil, errors.New("kubernetes: websocket: invalid control frame")
			}
			payload := make([]byte, n)
			if _, err := io.ReadFull(w.br, payload); err != nil {
				return nil, eof(err)
			}
			switch op {
			case opClose:
				w.writeFrame(opClose, nil) // best-effort reply
				return nil, io.EOF
			case opPing:
				if err := w.writeFrame(opPong, payload); err != nil {
					return nil, err
				}
			}
			continue
		}
		switch {
		case op == opContinuation && !started:
			return nil, errors.New("kubernetes: websocket: continuation without a message")
		case op != opContinuation && started:
			return nil, errors.New("kubernetes: websocket: new message inside a fragmented one")
		case op != opContinuation && op != opText && op != opBinary:
			return nil, fmt.Errorf("kubernetes: websocket: unknown opcode %d", op)
		}
		if n > maxMessage-uint64(len(msg)) {
			return nil, errMessageTooLarge
		}
		started = true
		start := len(msg)
		msg = slices.Grow(msg, int(n))[:start+int(n)]
		if _, err := io.ReadFull(w.br, msg[start:]); err != nil {
			return nil, eof(err)
		}
		if fin {
			return msg, nil
		}
	}
}

// writeFrame sends one final, masked frame (clients must mask, RFC 6455
// section 5.3). After a failed write the stream may hold half a frame, so
// every later write fails with the same error. Control replies from the
// read path (pong, close) share the lock, so a stalled data write delays
// them; that only slows a reader that is already blocked on the peer.
func (w *wsConn) writeFrame(op byte, payload []byte) error {
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	frame := make([]byte, 0, 14+len(payload))
	frame = append(frame, 0x80|op)
	switch n := len(payload); {
	case n < 126:
		frame = append(frame, 0x80|byte(n))
	case n <= 0xffff:
		frame = append(frame, 0x80|126)
		frame = binary.BigEndian.AppendUint16(frame, uint16(n))
	default:
		frame = append(frame, 0x80|127)
		frame = binary.BigEndian.AppendUint64(frame, uint64(n))
	}
	frame = append(frame, mask[:]...)
	for i, b := range payload {
		frame = append(frame, b^mask[i%4])
	}
	w.wmu.Lock()
	defer w.wmu.Unlock()
	if w.werr != nil {
		return w.werr
	}
	if _, err := w.conn.Write(frame); err != nil {
		w.werr = err
		return err
	}
	return nil
}

// Close sends a close frame, best effort, and closes the connection.
func (w *wsConn) Close() error {
	w.closeOnce.Do(func() {
		w.conn.SetWriteDeadline(time.Now().Add(time.Second))
		w.writeFrame(opClose, nil)
		w.closeErr = w.conn.Close()
	})
	return w.closeErr
}

// eof reports the peer ending the stream between frames as io.EOF. Every
// other error is kept: io.ErrUnexpectedEOF for an end mid-frame, and
// net.ErrClosed after a local Close.
func eof(err error) error {
	if errors.Is(err, io.EOF) {
		return io.EOF
	}
	return err
}

// loopback reports whether host is localhost or a loopback address.
func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
