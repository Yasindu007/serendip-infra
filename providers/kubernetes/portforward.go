package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Port-forward over the kubelet's WebSocket channel protocol
// (v4.channel.k8s.io), which the API server passes through unchanged. It
// lets serendipd, running outside the cluster, open a TCP connection to a
// pod port with every byte intact -- unlike the Service proxy, which
// strips the Authorization header (specs/018-agent-manager-poc plan.md
// decision 2 and T001 finding 3).
//
// Each message is one byte of channel index followed by payload. For the
// single forwarded port, channel 0 carries data both ways and channel 1
// carries error text from the server. The first message on each channel is
// the port number, two bytes little-endian, which is discarded.

const portForwardProtocol = "v4.channel.k8s.io"

// ErrNoReadyPod is returned when a Service has no ready pod to forward to.
var ErrNoReadyPod = errors.New("kubernetes: no ready pod behind the service")

// DialPod opens a TCP connection to port on pod in namespace through the
// API server's port-forward subresource. One call is one connection, so it
// plugs into http.Transport.DialContext. It needs create on pods/portforward.
func (c *Client) DialPod(ctx context.Context, namespace, pod string, port int) (net.Conn, error) {
	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward?ports=%d",
		url.PathEscape(namespace), url.PathEscape(pod), port)
	ws, err := c.dialWebSocket(ctx, path, portForwardProtocol)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: port-forward %s/%s:%d: %w", namespace, pod, port, err)
	}
	return &forwardConn{ws: ws, target: fmt.Sprintf("%s/%s:%d", namespace, pod, port)}, nil
}

// ServiceDialer returns a DialContext that forwards each new connection to
// a ready pod behind service, at the container port that the service's
// port maps to. The pod is looked up per connection, so a restarted pod is
// picked up without any state here. The network and address arguments are
// ignored: the destination is fixed.
func (c *Client) ServiceDialer(namespace, service string, port int) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, _, _ string) (net.Conn, error) {
		pod, target, err := c.ReadyPod(ctx, namespace, service, port)
		if err != nil {
			return nil, err
		}
		return c.DialPod(ctx, namespace, pod, target)
	}
}

type serviceSpec struct {
	Selector map[string]string `json:"selector"`
	Ports    []struct {
		Port       int             `json:"port"`
		TargetPort json.RawMessage `json:"targetPort"`
	} `json:"ports"`
}

type podInfo struct {
	Metadata struct {
		Name              string `json:"name"`
		DeletionTimestamp string `json:"deletionTimestamp"`
	} `json:"metadata"`
	Spec struct {
		Containers []struct {
			Ports []struct {
				Name          string `json:"name"`
				ContainerPort int    `json:"containerPort"`
			} `json:"ports"`
		} `json:"containers"`
	} `json:"spec"`
	Status struct {
		Phase      string         `json:"phase"`
		Conditions []podCondition `json:"conditions"`
	} `json:"status"`
}

type podCondition struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}

func (p podInfo) ready() bool {
	if p.Metadata.DeletionTimestamp != "" || p.Status.Phase != "Running" {
		return false
	}
	return slices.Contains(p.Status.Conditions, podCondition{Type: "Ready", Status: "True"})
}

// ReadyPod picks a ready pod selected by service in namespace (the first by
// name, so the choice is stable) and resolves port, a service port, to the
// container port it targets, by number or by name. It returns
// ErrNoReadyPod when none is ready, and ErrNotFound when the service
// doesn't exist.
func (c *Client) ReadyPod(ctx context.Context, namespace, service string, port int) (pod string, containerPort int, err error) {
	svc, err := c.Get(ctx, "/api/v1/namespaces/"+url.PathEscape(namespace)+"/services/"+url.PathEscape(service))
	if err != nil {
		return "", 0, fmt.Errorf("kubernetes: service %s/%s: %w", namespace, service, err)
	}
	var spec serviceSpec
	if err := json.Unmarshal(svc.Spec, &spec); err != nil {
		return "", 0, fmt.Errorf("kubernetes: service %s/%s: %w", namespace, service, err)
	}
	if len(spec.Selector) == 0 {
		return "", 0, fmt.Errorf("kubernetes: service %s/%s has no selector", namespace, service)
	}
	var target json.RawMessage
	found := false
	for _, p := range spec.Ports {
		if p.Port == port {
			target, found = p.TargetPort, true
			break
		}
	}
	if !found {
		return "", 0, fmt.Errorf("kubernetes: service %s/%s has no port %d", namespace, service, port)
	}

	keys := make([]string, 0, len(spec.Selector))
	for k := range spec.Selector {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	sel := make([]string, len(keys))
	for i, k := range keys {
		sel[i] = k + "=" + spec.Selector[k]
	}
	resp, err := c.do(ctx, http.MethodGet, "/api/v1/namespaces/"+url.PathEscape(namespace)+"/pods?labelSelector="+url.QueryEscape(strings.Join(sel, ",")), nil)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, unexpectedStatus(resp)
	}
	var list struct {
		Items []podInfo `json:"items"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&list); err != nil {
		return "", 0, fmt.Errorf("kubernetes: decode pods of %s/%s: %w", namespace, service, err)
	}
	slices.SortFunc(list.Items, func(a, b podInfo) int { return strings.Compare(a.Metadata.Name, b.Metadata.Name) })
	for _, p := range list.Items {
		if !p.ready() {
			continue
		}
		cp, err := resolveTargetPort(target, port, p)
		if err != nil {
			return "", 0, fmt.Errorf("kubernetes: service %s/%s: %w", namespace, service, err)
		}
		return p.Metadata.Name, cp, nil
	}
	return "", 0, fmt.Errorf("kubernetes: service %s/%s: %w", namespace, service, ErrNoReadyPod)
}

// resolveTargetPort maps a service port's targetPort (absent, a number or a
// container port name) to a container port number on pod.
func resolveTargetPort(target json.RawMessage, port int, pod podInfo) (int, error) {
	if len(target) == 0 {
		return port, nil // targetPort defaults to port
	}
	var n int
	if err := json.Unmarshal(target, &n); err == nil {
		return n, nil
	}
	var name string
	if err := json.Unmarshal(target, &name); err != nil {
		return 0, fmt.Errorf("targetPort %s: %w", target, err)
	}
	if v, err := strconv.Atoi(name); err == nil {
		return v, nil
	}
	for _, ctr := range pod.Spec.Containers {
		for _, p := range ctr.Ports {
			if p.Name == name {
				return p.ContainerPort, nil
			}
		}
	}
	return 0, fmt.Errorf("pod %s has no container port named %q", pod.Metadata.Name, name)
}

// forwardConn is one forwarded TCP connection, as a net.Conn.
type forwardConn struct {
	ws     *wsConn
	target string

	rmu      sync.Mutex // guards the read side below
	buf      []byte
	preamble [2]bool // port number seen on data / error channel
	errText  []byte
	readErr  error
}

var _ net.Conn = (*forwardConn)(nil)

func (f *forwardConn) Read(b []byte) (int, error) {
	f.rmu.Lock()
	defer f.rmu.Unlock()
	for len(f.buf) == 0 {
		if f.readErr != nil {
			return 0, f.readErr
		}
		msg, err := f.ws.readMessage()
		if err != nil {
			if len(f.errText) > 0 {
				// The server explains a failed forward on the error channel,
				// then closes. Its text names pod internals, so callers log it
				// rather than show it; %q keeps it to one log line.
				err = fmt.Errorf("kubernetes: port-forward %s: %q", f.target, f.errText)
			}
			// Latched: an error mid-frame (a read deadline included) leaves
			// the stream out of step, so the connection is done.
			f.readErr = err
			continue
		}
		if len(msg) == 0 {
			continue
		}
		ch, data := msg[0], msg[1:]
		if ch > 1 {
			continue // only one port is forwarded
		}
		if !f.preamble[ch] {
			f.preamble[ch] = true
			if len(data) < 2 {
				f.readErr = errors.New("kubernetes: port-forward: short port preamble")
				continue
			}
			data = data[2:]
		}
		if ch == 1 {
			if len(f.errText)+len(data) <= 4096 {
				f.errText = append(f.errText, data...)
			}
			continue
		}
		f.buf = append(f.buf, data...)
	}
	n := copy(b, f.buf)
	f.buf = f.buf[n:]
	if len(f.buf) == 0 {
		f.buf = nil
	}
	return n, nil
}

// maxWriteChunk keeps each outgoing frame modest, whatever size the caller
// writes.
const maxWriteChunk = 32 << 10

func (f *forwardConn) Write(b []byte) (int, error) {
	written := 0
	for len(b) > 0 {
		chunk := b[:min(len(b), maxWriteChunk)]
		payload := make([]byte, 1+len(chunk))
		copy(payload[1:], chunk) // channel 0
		if err := f.ws.writeFrame(opBinary, payload); err != nil {
			return written, err
		}
		written += len(chunk)
		b = b[len(chunk):]
	}
	return written, nil
}

func (f *forwardConn) Close() error                       { return f.ws.Close() }
func (f *forwardConn) LocalAddr() net.Addr                { return f.ws.conn.LocalAddr() }
func (f *forwardConn) RemoteAddr() net.Addr               { return f.ws.conn.RemoteAddr() }
func (f *forwardConn) SetDeadline(t time.Time) error      { return f.ws.conn.SetDeadline(t) }
func (f *forwardConn) SetReadDeadline(t time.Time) error  { return f.ws.conn.SetReadDeadline(t) }
func (f *forwardConn) SetWriteDeadline(t time.Time) error { return f.ws.conn.SetWriteDeadline(t) }
