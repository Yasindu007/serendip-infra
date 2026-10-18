package kubernetes

import (
	"bufio"
	"context"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// wsPeer is the server side of a WebSocket, for tests: it reads masked
// client frames and writes unmasked ones.
type wsPeer struct {
	conn net.Conn
	br   *bufio.Reader
	// unmasked is set when a client frame arrives without a mask.
	unmasked bool
}

func (p *wsPeer) write(op byte, fin bool, payload []byte) {
	b := byte(op)
	if fin {
		b |= 0x80
	}
	frame := []byte{b}
	switch n := len(payload); {
	case n < 126:
		frame = append(frame, byte(n))
	case n <= 0xffff:
		frame = append(frame, 126)
		frame = binary.BigEndian.AppendUint16(frame, uint16(n))
	default:
		frame = append(frame, 127)
		frame = binary.BigEndian.AppendUint64(frame, uint64(n))
	}
	p.conn.Write(append(frame, payload...))
}

// send writes one data message on channel ch.
func (p *wsPeer) send(ch byte, data []byte) { p.write(opBinary, true, append([]byte{ch}, data...)) }

// read returns the next client frame's opcode and unmasked payload, or
// opcode 0 when the connection ends.
func (p *wsPeer) read() (byte, []byte) {
	var h [2]byte
	if _, err := io.ReadFull(p.br, h[:]); err != nil {
		return 0, nil
	}
	if h[1]&0x80 == 0 {
		p.unmasked = true
	}
	n := uint64(h[1] & 0x7f)
	switch n {
	case 126:
		var b [2]byte
		io.ReadFull(p.br, b[:])
		n = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		io.ReadFull(p.br, b[:])
		n = binary.BigEndian.Uint64(b[:])
	}
	var mask [4]byte
	io.ReadFull(p.br, mask[:])
	payload := make([]byte, n)
	io.ReadFull(p.br, payload)
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	return h[0] & 0x0f, payload
}

type fakeAPI struct {
	// accept, when set, replaces the correct Sec-WebSocket-Accept value.
	accept string
	// protocol is the subprotocol echoed back; defaults to the offered one.
	protocol string
	// status, when set, refuses the upgrade with that status.
	status int
	// gotPath, gotAuth and gotProto record the upgrade request.
	gotPath, gotAuth, gotProto chan string
	// session runs on the upgraded connection.
	session func(p *wsPeer)
}

func newFakeAPI(f *fakeAPI) http.HandlerFunc {
	f.gotPath, f.gotAuth, f.gotProto = make(chan string, 8), make(chan string, 8), make(chan string, 8)
	return func(w http.ResponseWriter, r *http.Request) {
		f.gotPath <- r.URL.RequestURI()
		f.gotAuth <- r.Header.Get("Authorization")
		f.gotProto <- r.Header.Get("Sec-WebSocket-Protocol")
		if f.status != 0 {
			w.WriteHeader(f.status)
			w.Write([]byte(`{"kind":"Status","message":"pods \"x\" not found"}`))
			return
		}
		if r.Header.Get("Upgrade") != "websocket" || r.Header.Get("Sec-WebSocket-Version") != "13" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		sum := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + wsGUID))
		accept := base64.StdEncoding.EncodeToString(sum[:])
		if f.accept != "" {
			accept = f.accept
		}
		proto := r.Header.Get("Sec-WebSocket-Protocol")
		if f.protocol != "" {
			proto = f.protocol
		}
		conn, brw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		brw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: " + accept + "\r\nSec-WebSocket-Protocol: " + proto + "\r\n\r\n")
		brw.Flush()
		if f.session != nil {
			f.session(&wsPeer{conn: conn, br: brw.Reader})
		}
	}
}

func preamble(port int) []byte { return binary.LittleEndian.AppendUint16(nil, uint16(port)) }

// echoSession sends the port preambles, then answers each data message
// with its upper-cased payload, until the client closes.
func echoSession(t *testing.T) func(p *wsPeer) {
	return func(p *wsPeer) {
		p.send(0, preamble(8090))
		p.send(1, preamble(8090))
		for {
			op, payload := p.read()
			if p.unmasked {
				t.Error("client frame is not masked")
			}
			switch op {
			case opBinary:
				if len(payload) == 0 || payload[0] != 0 {
					t.Errorf("client wrote on channel %v, want 0", payload[:min(1, len(payload))])
					return
				}
				p.send(0, []byte(strings.ToUpper(string(payload[1:]))))
			case opClose:
				p.write(opClose, true, nil)
				return
			default:
				return
			}
		}
	}
}

func plainClient(t *testing.T, h http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Client{baseURL: srv.URL, http: srv.Client()}
}

func dialCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestDialPod_RoundTripOverTLSWithBearer(t *testing.T) {
	f := &fakeAPI{}
	f.session = echoSession(t)
	srv := httptest.NewTLSServer(newFakeAPI(f))
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	c := &Client{baseURL: srv.URL, token: "kube-token", tls: &tls.Config{RootCAs: pool}}

	conn, err := c.DialPod(dialCtx(t), "serendip-identity", "thunderid-0", 8090)
	if err != nil {
		t.Fatalf("DialPod() = %v", err)
	}
	defer conn.Close()
	if got := <-f.gotPath; got != "/api/v1/namespaces/serendip-identity/pods/thunderid-0/portforward?ports=8090" {
		t.Errorf("path = %q", got)
	}
	if got := <-f.gotAuth; got != "Bearer kube-token" {
		t.Errorf("upgrade Authorization = %q, want the client's bearer token", got)
	}
	if got := <-f.gotProto; got != portForwardProtocol {
		t.Errorf("offered subprotocol = %q, want %q", got, portForwardProtocol)
	}
	// Bytes, including an Authorization header inside the tunnel, pass
	// through untouched: that is the point of the transport.
	req := "GET /users HTTP/1.1\r\nAuthorization: Bearer inner\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(req))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("read = %v", err)
	}
	if string(got) != strings.ToUpper(req) {
		t.Errorf("echo = %q, want %q", got, strings.ToUpper(req))
	}
}

func TestDialPod_LargeWritesAreChunkedAndReassembled(t *testing.T) {
	f := &fakeAPI{}
	f.session = echoSession(t)
	c := plainClient(t, newFakeAPI(f))
	conn, err := c.DialPod(dialCtx(t), "ns", "p", 80)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	big := strings.Repeat("a", 3*maxWriteChunk+17)
	if n, err := conn.Write([]byte(big)); err != nil || n != len(big) {
		t.Fatalf("Write = %d, %v", n, err)
	}
	got := make([]byte, len(big))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != strings.ToUpper(big) {
		t.Error("large payload did not round-trip")
	}
}

func TestDialPod_HandshakeFailures(t *testing.T) {
	tests := []struct {
		name string
		f    *fakeAPI
		want string
		is   error
	}{
		{name: "bad accept", f: &fakeAPI{accept: "bm90LXRoZS1yaWdodC1rZXk="}, want: "Sec-WebSocket-Accept"},
		{name: "wrong subprotocol", f: &fakeAPI{protocol: "v5.channel.k8s.io"}, want: "subprotocol"},
		{name: "pod not found", f: &fakeAPI{status: http.StatusNotFound}, is: ErrNotFound},
		{name: "forbidden", f: &fakeAPI{status: http.StatusForbidden}, want: "403"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := plainClient(t, newFakeAPI(tt.f))
			_, err := c.DialPod(dialCtx(t), "ns", "p", 80)
			if err == nil {
				t.Fatal("DialPod() succeeded, want an error")
			}
			if tt.is != nil && !errors.Is(err, tt.is) {
				t.Errorf("err = %v, want %v", err, tt.is)
			}
			if tt.want != "" && !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestDialPod_ServerMessages(t *testing.T) {
	tests := []struct {
		name    string
		session func(p *wsPeer)
		want    string // expected data before the error
		wantErr string // "" means io.EOF
	}{
		{
			name: "error channel explains a failed forward",
			session: func(p *wsPeer) {
				p.send(0, preamble(81))
				p.send(1, preamble(81))
				p.send(1, []byte("error forwarding port 81 to pod abc: connection refused"))
				p.write(opClose, true, nil)
			},
			wantErr: "connection refused",
		},
		{
			name: "fragmented message is reassembled, ping answered",
			session: func(p *wsPeer) {
				p.send(0, preamble(80))
				p.send(1, preamble(80))
				p.write(opBinary, false, []byte{0, 'h', 'e'})
				p.write(opPing, true, []byte("hi"))
				p.write(opContinuation, true, []byte("llo"))
				op, payload := p.read()
				if op != opPong || string(payload) != "hi" {
					p.send(0, []byte("no pong"))
				}
				p.write(opClose, true, nil)
			},
			want: "hello",
		},
		{
			name: "masked server frame",
			session: func(p *wsPeer) {
				p.conn.Write([]byte{0x82, 0x81, 1, 2, 3, 4, 5})
			},
			wantErr: "masked",
		},
		{
			name: "oversize message",
			session: func(p *wsPeer) {
				p.send(0, preamble(80))
				frame := []byte{0x82, 127}
				frame = binary.BigEndian.AppendUint64(frame, maxMessage+1)
				p.conn.Write(frame)
			},
			wantErr: "too large",
		},
		{
			name: "new message inside a fragmented one",
			session: func(p *wsPeer) {
				p.send(0, preamble(80))
				p.write(opBinary, false, []byte{0, 'a'})
				p.write(opBinary, true, []byte{0, 'b'})
			},
			wantErr: "inside a fragmented",
		},
		{
			name: "oversize control frame",
			session: func(p *wsPeer) {
				p.send(0, preamble(80))
				p.write(opPing, true, make([]byte, 126))
			},
			wantErr: "invalid control frame",
		},
		{
			name: "fragmented control frame",
			session: func(p *wsPeer) {
				p.send(0, preamble(80))
				p.write(opPing, false, nil)
			},
			wantErr: "invalid control frame",
		},
		{
			name: "short preamble",
			session: func(p *wsPeer) {
				p.send(0, []byte{1})
			},
			wantErr: "preamble",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeAPI{session: tt.session}
			c := plainClient(t, newFakeAPI(f))
			conn, err := c.DialPod(dialCtx(t), "ns", "p", 80)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			got, err := io.ReadAll(conn)
			if string(got) != tt.want {
				t.Errorf("data = %q, want %q", got, tt.want)
			}
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("ReadAll err = %v, want a clean close", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Errorf("ReadAll err = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

func TestDialPod_CloseSendsCloseFrame(t *testing.T) {
	gotClose := make(chan bool, 1)
	f := &fakeAPI{session: func(p *wsPeer) {
		p.send(0, preamble(80))
		op, _ := p.read()
		gotClose <- op == opClose
	}}
	c := plainClient(t, newFakeAPI(f))
	conn, err := c.DialPod(dialCtx(t), "ns", "p", 80)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Errorf("second Close() = %v, want nil", err)
	}
	select {
	case ok := <-gotClose:
		if !ok {
			t.Error("server did not receive a close frame")
		}
	case <-time.After(5 * time.Second):
		t.Error("server saw nothing")
	}
}

func TestDialPod_CancelAbortsStalledHandshake(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	c := plainClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		<-release // never answer the upgrade
	}))
	ctx, cancel := context.WithCancel(context.Background()) // no deadline
	time.AfterFunc(100*time.Millisecond, cancel)
	done := make(chan error, 1)
	go func() {
		_, err := c.DialPod(ctx, "ns", "p", 80)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("DialPod still blocked after its context was cancelled")
	}
}

func TestDialPod_AfterLocalClose(t *testing.T) {
	f := &fakeAPI{}
	f.session = echoSession(t)
	c := plainClient(t, newFakeAPI(f))
	conn, err := c.DialPod(dialCtx(t), "ns", "p", 80)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if _, err := conn.Write([]byte("x")); err == nil {
		t.Error("Write after Close succeeded")
	}
	if _, err := conn.Read(make([]byte, 1)); !errors.Is(err, net.ErrClosed) {
		t.Errorf("Read after Close = %v, want net.ErrClosed", err)
	}
}

func TestDialPod_RefusesBearerOverPlainHTTPToRemoteHost(t *testing.T) {
	c := &Client{baseURL: "http://kube.example.invalid:6443", token: "kube-token"}
	_, err := c.DialPod(dialCtx(t), "ns", "p", 80)
	if err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Errorf("err = %v, want a refusal to send the token in cleartext", err)
	}
}

func TestReadyPod(t *testing.T) {
	pod := func(name, phase, ready string, deleting bool, ports ...any) map[string]any {
		meta := map[string]any{"name": name}
		if deleting {
			meta["deletionTimestamp"] = "2026-10-18T00:00:00Z"
		}
		var cports []any
		for i := 0; i+1 < len(ports); i += 2 {
			cports = append(cports, map[string]any{"name": ports[i], "containerPort": ports[i+1]})
		}
		return map[string]any{
			"metadata": meta,
			"spec":     map[string]any{"containers": []any{map[string]any{"ports": cports}}},
			"status": map[string]any{"phase": phase, "conditions": []any{
				map[string]any{"type": "Ready", "status": ready},
			}},
		}
	}
	tests := []struct {
		name       string
		targetPort any // nil = absent
		pods       []map[string]any
		wantPod    string
		wantPort   int
		wantErr    error
		wantSubstr string
	}{
		{
			name:       "first ready pod by name, numeric target",
			targetPort: 9090,
			pods: []map[string]any{
				pod("b", "Running", "True", false),
				pod("a", "Running", "True", false),
			},
			wantPod: "a", wantPort: 9090,
		},
		{
			name: "skips unready, terminating and pending pods",
			pods: []map[string]any{
				pod("a", "Running", "False", false),
				pod("b", "Running", "True", true),
				pod("c", "Pending", "True", false),
				pod("d", "Running", "True", false),
			},
			wantPod: "d", wantPort: 8090,
		},
		{
			name:       "named target port",
			targetPort: "http",
			pods:       []map[string]any{pod("a", "Running", "True", false, "metrics", 9100, "http", 8091)},
			wantPod:    "a", wantPort: 8091,
		},
		{
			name:       "unknown named target port",
			targetPort: "grpc",
			pods:       []map[string]any{pod("a", "Running", "True", false, "http", 8091)},
			wantSubstr: `no container port named "grpc"`,
		},
		{
			name:    "no ready pod",
			pods:    []map[string]any{pod("a", "Running", "False", false)},
			wantErr: ErrNoReadyPod,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotSelector string
			c, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/namespaces/serendip-identity/services/thunderid-service":
					port := map[string]any{"port": 8090}
					if tt.targetPort != nil {
						port["targetPort"] = tt.targetPort
					}
					json.NewEncoder(w).Encode(map[string]any{
						"apiVersion": "v1", "kind": "Service", "metadata": map[string]any{"name": "thunderid-service"},
						"spec": map[string]any{"selector": map[string]string{"app": "thunderid", "tier": "server"}, "ports": []any{port}},
					})
				case "/api/v1/namespaces/serendip-identity/pods":
					gotSelector = r.URL.Query().Get("labelSelector")
					json.NewEncoder(w).Encode(map[string]any{"items": tt.pods})
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			})
			name, port, err := c.ReadyPod(context.Background(), "serendip-identity", "thunderid-service", 8090)
			switch {
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			case tt.wantSubstr != "":
				if err == nil || !strings.Contains(err.Error(), tt.wantSubstr) {
					t.Fatalf("err = %v, want it to mention %q", err, tt.wantSubstr)
				}
				return
			case err != nil:
				t.Fatalf("ReadyPod() = %v", err)
			}
			if gotSelector != "app=thunderid,tier=server" {
				t.Errorf("labelSelector = %q, want sorted app=thunderid,tier=server", gotSelector)
			}
			if name != tt.wantPod || port != tt.wantPort {
				t.Errorf("ReadyPod() = %s:%d, want %s:%d", name, port, tt.wantPod, tt.wantPort)
			}
		})
	}
}

func TestReadyPod_ServiceErrors(t *testing.T) {
	t.Run("missing service", func(t *testing.T) {
		c, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })
		if _, _, err := c.ReadyPod(context.Background(), "ns", "svc", 80); !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})
	t.Run("port not on service", func(t *testing.T) {
		c, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"spec": map[string]any{"selector": map[string]string{"a": "b"}, "ports": []any{map[string]any{"port": 81}}}})
		})
		if _, _, err := c.ReadyPod(context.Background(), "ns", "svc", 80); err == nil || !strings.Contains(err.Error(), "no port 80") {
			t.Errorf("err = %v, want no port 80", err)
		}
	})
	t.Run("no selector", func(t *testing.T) {
		c, _ := newTestServerClient(t, func(w http.ResponseWriter, r *http.Request) {
			json.NewEncoder(w).Encode(map[string]any{"spec": map[string]any{"ports": []any{map[string]any{"port": 80}}}})
		})
		if _, _, err := c.ReadyPod(context.Background(), "ns", "svc", 80); err == nil || !strings.Contains(err.Error(), "no selector") {
			t.Errorf("err = %v, want no selector", err)
		}
	})
}

func TestServiceDialer_HTTPThroughTunnel(t *testing.T) {
	// The fake API serves the Service and pod lookups, then forwards the
	// tunnel to a real HTTP handler, proving http.Transport works on top.
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("auth=" + r.Header.Get("Authorization")))
	})
	var api http.HandlerFunc
	f := &fakeAPI{session: func(p *wsPeer) {
		p.send(0, preamble(8090))
		p.send(1, preamble(8090))
		client, server := net.Pipe()
		go http.Serve(&oneConnListener{conn: server}, backend)
		go func() {
			for {
				op, payload := p.read()
				if op != opBinary {
					client.Close()
					return
				}
				client.Write(payload[1:])
			}
		}()
		buf := make([]byte, 4096)
		for {
			n, err := client.Read(buf)
			if n > 0 {
				p.send(0, buf[:n])
			}
			if err != nil {
				return
			}
		}
	}}
	upgrade := newFakeAPI(f)
	api = func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/namespaces/serendip-identity/services/thunderid-service":
			json.NewEncoder(w).Encode(map[string]any{"spec": map[string]any{"selector": map[string]string{"app": "thunderid"}, "ports": []any{map[string]any{"port": 8090}}}})
		case r.URL.Path == "/api/v1/namespaces/serendip-identity/pods":
			json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{
				"metadata": map[string]any{"name": "thunderid-0"},
				"status":   map[string]any{"phase": "Running", "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}},
			}}})
		default:
			upgrade(w, r)
		}
	}
	c := plainClient(t, api)
	hc := &http.Client{Transport: &http.Transport{DialContext: c.ServiceDialer("serendip-identity", "thunderid-service", 8090)}, Timeout: 5 * time.Second}
	// Two requests: the second reuses the kept-alive tunnel.
	for i := range 2 {
		req, _ := http.NewRequest(http.MethodGet, "http://thunderid/users", nil)
		req.Header.Set("Authorization", "Bearer management-token")
		resp, err := hc.Do(req)
		if err != nil {
			t.Fatalf("GET #%d through the tunnel = %v", i+1, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != "auth=Bearer management-token" {
			t.Errorf("backend saw %q, want the Authorization header intact", body)
		}
	}
	if got := <-f.gotPath; got != "/api/v1/namespaces/serendip-identity/pods/thunderid-0/portforward?ports=8090" {
		t.Errorf("forwarded to %q", got)
	}
	if n := len(f.gotPath); n != 0 {
		t.Errorf("%d more tunnels opened, want the kept-alive one reused", n)
	}
}

// oneConnListener hands http.Serve a single connection.
type oneConnListener struct {
	conn net.Conn
	done bool
}

func (l *oneConnListener) Accept() (net.Conn, error) {
	if l.done {
		select {} // block: http.Serve then never returns, harmless in a test
	}
	l.done = true
	return l.conn, nil
}
func (l *oneConnListener) Close() error   { return nil }
func (l *oneConnListener) Addr() net.Addr { return l.conn.LocalAddr() }
