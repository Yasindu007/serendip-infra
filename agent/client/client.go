package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrNotFound and ErrConflict mirror the two HTTP statuses this client
// needs to distinguish -- 404 (create instead of update) and 409 (a
// concurrent edit raced this Upsert call, worth one retry).
var (
	ErrNotFound = errors.New("client: resource not found")
	ErrConflict = errors.New("client: resource version conflict")
)

// MetadataWire mirrors core/resource.Metadata's JSON shape for the one
// resource kind this client speaks (Node). A redefinition, not an
// import of core/resource -- agent/ stays a true "just another API
// client" (specs/004-bootstrap-agent/plan.md's resolved wire-format
// decision, constitution rule 7). Labels/OwnerRefs/Relationships are
// carried even though nothing sets them on a Node today, purely so a
// round-tripped GET-then-PUT preserves them rather than silently
// wiping them if something does set them later.
type MetadataWire struct {
	ID              string             `json:"id"`
	Name            string             `json:"name"`
	Kind            string             `json:"kind,omitempty"`
	Labels          map[string]string  `json:"labels,omitempty"`
	OwnerRefs       []OwnerRefWire     `json:"ownerRefs,omitempty"`
	Relationships   []RelationshipWire `json:"relationships,omitempty"`
	ResourceVersion int64              `json:"resourceVersion"`
}

type OwnerRefWire struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type RelationshipWire struct {
	Type       string `json:"type"`
	TargetKind string `json:"targetKind"`
	TargetID   string `json:"targetId"`
}

// ConditionWire mirrors core/resource.Condition's JSON shape. This
// agent never sets Conditions itself, but Upsert preserves whatever a
// future setter (e.g. a Node reconciler) puts there -- same
// round-trip-preservation reasoning as MetadataWire's Labels/OwnerRefs/
// Relationships above.
type ConditionWire struct {
	Type               string    `json:"type"`
	Status             string    `json:"status"`
	Reason             string    `json:"reason,omitempty"`
	Message            string    `json:"message,omitempty"`
	LastTransitionTime time.Time `json:"lastTransitionTime"`
}

// NodeStatusWire mirrors core/resource.NodeStatus's JSON shape.
type NodeStatusWire struct {
	Conditions            []ConditionWire `json:"conditions,omitempty"`
	CPUCores              int             `json:"cpuCores,omitempty"`
	MemoryBytes           int64           `json:"memoryBytes,omitempty"`
	DiskType              string          `json:"diskType,omitempty"`
	DiskBytes             int64           `json:"diskBytes,omitempty"`
	NetworkMbps           int             `json:"networkMbps,omitempty"`
	VirtualizationSupport bool            `json:"virtualizationSupport,omitempty"`
}

// NodeWire mirrors core/resource.Resource[NodeSpec, NodeStatus]'s JSON
// shape. NodeSpec has no fields (core/resource/infra.go), so Spec is
// just an empty object on the wire.
type NodeWire struct {
	Metadata MetadataWire   `json:"metadata"`
	Spec     struct{}       `json:"spec"`
	Status   NodeStatusWire `json:"status"`
}

// Client is a minimal net/http client for one Serendip control plane's
// /v1/nodes routes (spec 002), authenticated via the same static bearer
// token scheme api.ActorMiddleware already implements.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// Upsert creates the Node identified by id if it doesn't exist yet, or
// updates it (Name and the hardware fields of Status only -- everything
// else, including any Conditions a future setter puts on Status,
// round-trips unchanged) if it does. One retry on a 409 conflict from
// the update path: a one-shot CLI invocation doesn't need an unbounded
// retry loop (plan.md Risks) -- the realistic case this handles is a
// stale ResourceVersion left over from a slow-running previous
// invocation, not true concurrent registration.
func (c *Client) Upsert(ctx context.Context, id, name string, status NodeStatusWire) (NodeWire, error) {
	existing, err := c.get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return c.create(ctx, id, name, status)
	}
	if err != nil {
		return NodeWire{}, err
	}

	status.Conditions = existing.Status.Conditions
	existing.Metadata.Name = name
	existing.Status = status
	updated, err := c.update(ctx, existing)
	if errors.Is(err, ErrConflict) {
		existing, err = c.get(ctx, id)
		if err != nil {
			return NodeWire{}, fmt.Errorf("client: retry after conflict: %w", err)
		}
		status.Conditions = existing.Status.Conditions
		existing.Metadata.Name = name
		existing.Status = status
		return c.update(ctx, existing)
	}
	return updated, err
}

func (c *Client) get(ctx context.Context, id string) (NodeWire, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/nodes/"+id, nil)
	if err != nil {
		return NodeWire{}, fmt.Errorf("client: build GET request: %w", err)
	}
	c.setAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return NodeWire{}, fmt.Errorf("client: GET /v1/nodes/%s: %w", id, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var out NodeWire
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return NodeWire{}, fmt.Errorf("client: decode GET response: %w", err)
		}
		return out, nil
	case http.StatusNotFound:
		return NodeWire{}, ErrNotFound
	default:
		return NodeWire{}, unexpectedStatus(resp)
	}
}

func (c *Client) create(ctx context.Context, id, name string, status NodeStatusWire) (NodeWire, error) {
	body := NodeWire{
		Metadata: MetadataWire{ID: id, Name: name},
		Status:   status,
	}
	return c.send(ctx, http.MethodPost, c.baseURL+"/v1/nodes", body, http.StatusCreated)
}

func (c *Client) update(ctx context.Context, node NodeWire) (NodeWire, error) {
	return c.send(ctx, http.MethodPut, c.baseURL+"/v1/nodes/"+node.Metadata.ID, node, http.StatusOK)
}

func (c *Client) send(ctx context.Context, method, url string, body any, wantStatus int) (NodeWire, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return NodeWire{}, fmt.Errorf("client: marshal request body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(buf))
	if err != nil {
		return NodeWire{}, fmt.Errorf("client: build %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return NodeWire{}, fmt.Errorf("client: %s %s: %w", method, url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusConflict {
		return NodeWire{}, ErrConflict
	}
	if resp.StatusCode != wantStatus {
		return NodeWire{}, unexpectedStatus(resp)
	}

	var out NodeWire
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return NodeWire{}, fmt.Errorf("client: decode %s response: %w", method, err)
	}
	return out, nil
}

func (c *Client) setAuth(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}

func unexpectedStatus(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("client: unexpected status %d: %s", resp.StatusCode, string(body))
}
