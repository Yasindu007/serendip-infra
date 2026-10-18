package thunderid

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/serendip-infra/serendip-infra/core/identity"
	"github.com/serendip-infra/serendip-infra/core/reconcile"
	"github.com/serendip-infra/serendip-infra/providers/kubernetes"
)

// The directory reads ThunderID's management API through a port-forward
// (kubernetes.Client.ServiceDialer), the one channel that keeps the
// Authorization header (specs/018-agent-manager-poc plan.md decisions 2-4),
// as the bootstrap administrator: serendipd already deploys ThunderID and
// holds that password, so this adds no privilege. Only reads are made.

const (
	// pageSize is the largest page ThunderID 1.0.1 serves.
	pageSize = 100
	// maxScanned bounds how many people or groups one search reads. The
	// management API filters by exact match only, so substring search
	// pages through the directory itself.
	maxScanned = 1000
	// maxResults bounds what one search returns.
	maxResults = 50
	// tokenSlack renews the admin token this long before it expires.
	tokenSlack = time.Minute
	// fallbackTokenLife is assumed when the token response has no
	// expires_in.
	fallbackTokenLife = 5 * time.Minute
)

// tunnelBase is the in-tunnel origin: the dialer ignores the address, so
// the host only names the request.
const tunnelBase = "http://thunderid"

// Directory is the Identity capability's identity.Directory for ThunderID.
// Safe for concurrent use.
type Directory struct {
	client *kubernetes.Client
	issuer string
	hc     *http.Client
	now    func() time.Time

	loginMu sync.Mutex // one admin login at a time
	mu      sync.Mutex
	token   string
	expiry  time.Time
}

var _ identity.Directory = (*Directory)(nil)

// NewDirectory returns a Directory reaching ThunderID through client. issuer
// is the public issuer URL, which the admin login's redirect URI is built
// from. It does no I/O.
func NewDirectory(client *kubernetes.Client, issuer string) *Directory {
	return &Directory{
		client: client,
		issuer: issuer,
		hc: &http.Client{
			Transport: &http.Transport{
				DialContext: client.ServiceDialer(Namespace, ServiceName, ServicePort),
				// Idle tunnels are dropped soon, so a restarted pod is picked up
				// by the next connection's lookup.
				IdleConnTimeout: 30 * time.Second,
				MaxIdleConns:    2,
			},
			Timeout: 15 * time.Second,
		},
		now: time.Now,
	}
}

type userPage struct {
	TotalResults int `json:"totalResults"`
	Users        []struct {
		ID         string         `json:"id"`
		Attributes map[string]any `json:"attributes"`
	} `json:"users"`
}

type groupPage struct {
	TotalResults int `json:"totalResults"`
	Groups       []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"groups"`
}

// People searches people by username, name, given and family name, and
// email.
func (d *Directory) People(ctx context.Context, query string) (identity.DirectoryResult, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	var res identity.DirectoryResult
	for offset := 0; ; offset += pageSize {
		var page userPage
		if err := d.get(ctx, fmt.Sprintf("/users?limit=%d&offset=%d", pageSize, offset), &page, false); err != nil {
			return identity.DirectoryResult{}, err
		}
		for _, u := range page.Users {
			attr := func(k string) string { s, _ := u.Attributes[k].(string); return s }
			name := firstNonEmpty(attr("username"), attr("name"), attr("email"), u.ID)
			if q == "" || containsFold(q, attr("username"), attr("name"), attr("given_name"), attr("family_name"), attr("email")) {
				if !add(&res, identity.DirectoryEntry{ID: u.ID, Name: name}) {
					return res, nil
				}
			}
		}
		if done(&res, offset, len(page.Users), page.TotalResults) {
			return res, nil
		}
	}
}

// Groups searches groups by name.
func (d *Directory) Groups(ctx context.Context, query string) (identity.DirectoryResult, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	var res identity.DirectoryResult
	for offset := 0; ; offset += pageSize {
		var page groupPage
		if err := d.get(ctx, fmt.Sprintf("/groups?limit=%d&offset=%d", pageSize, offset), &page, false); err != nil {
			return identity.DirectoryResult{}, err
		}
		for _, g := range page.Groups {
			if q == "" || containsFold(q, g.Name) {
				if !add(&res, identity.DirectoryEntry{ID: g.Name, Name: g.Name}) {
					return res, nil
				}
			}
		}
		if done(&res, offset, len(page.Groups), page.TotalResults) {
			return res, nil
		}
	}
}

// GroupsOf returns the names of subject's direct groups. More than
// maxScanned is an error rather than a partial list, which would understate
// the person's access.
func (d *Directory) GroupsOf(ctx context.Context, subject string) ([]string, error) {
	// A subject is one path segment: "." and ".." would be resolved as path
	// steps by anything that normalises paths, and PathEscape keeps them.
	if subject == "" || subject == "." || subject == ".." || strings.Contains(subject, "/") {
		return nil, identity.ErrUnknownSubject
	}
	var names []string
	for offset := 0; ; offset += pageSize {
		if offset >= maxScanned {
			return nil, fmt.Errorf("%w: thunderid directory: %s is in more than %d groups", reconcile.ErrProviderNotReady, subject, maxScanned)
		}
		var page groupPage
		err := d.get(ctx, fmt.Sprintf("/users/%s/groups?limit=%d&offset=%d", url.PathEscape(subject), pageSize, offset), &page, true)
		var se *statusError
		if errors.As(err, &se) && se.code == http.StatusNotFound {
			return nil, identity.ErrUnknownSubject
		}
		if err != nil {
			return nil, err
		}
		for _, g := range page.Groups {
			names = append(names, g.Name)
		}
		if lastPage(offset, len(page.Groups), page.TotalResults) {
			return names, nil
		}
	}
}

// get GETs path from the management API into out, logging in first if
// needed and once more if the cached token is refused. Every failure wraps
// reconcile.ErrProviderNotReady, keeping its cause, except a 404 when
// want404 is set, which is returned as a *statusError for the caller to
// inspect.
func (d *Directory) get(ctx context.Context, path string, out any, want404 bool) error {
	for attempt := 0; ; attempt++ {
		token, err := d.adminToken(ctx)
		if err != nil {
			return err
		}
		err = callJSON(ctx, d.hc, http.MethodGet, tunnelBase+path, token, nil, out)
		var se *statusError
		switch {
		case err == nil:
			return nil
		case errors.As(err, &se) && se.code == http.StatusUnauthorized && attempt == 0:
			d.forget(token)
			continue
		case want404 && errors.As(err, &se) && se.code == http.StatusNotFound:
			return err
		default:
			return fmt.Errorf("%w: thunderid directory: %w", reconcile.ErrProviderNotReady, err)
		}
	}
}

// adminToken returns a cached administrator token, logging in when there
// is none or it is about to expire.
func (d *Directory) adminToken(ctx context.Context) (string, error) {
	if tok, ok := d.cached(); ok {
		return tok, nil
	}
	d.loginMu.Lock()
	defer d.loginMu.Unlock()
	if tok, ok := d.cached(); ok { // another caller logged in meanwhile
		return tok, nil
	}
	password, err := d.adminPassword(ctx)
	if err != nil {
		return "", fmt.Errorf("%w: thunderid directory: %w", reconcile.ErrProviderNotReady, err)
	}
	tok, life, err := managementToken(ctx, tunnelBase, noRedirects(d.hc), d.issuer, password)
	if err != nil {
		return "", fmt.Errorf("%w: thunderid directory: %w", reconcile.ErrProviderNotReady, err)
	}
	if life <= 0 {
		life = fallbackTokenLife
	}
	// Renew tokenSlack early; a token too short-lived for that is kept for
	// half its life, so it is still reused rather than logging in per call.
	keep := life - tokenSlack
	if life <= 2*tokenSlack {
		keep = life / 2
	}
	d.mu.Lock()
	d.token, d.expiry = tok, d.now().Add(keep)
	d.mu.Unlock()
	return tok, nil
}

func (d *Directory) cached() (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.token, d.token != "" && d.now().Before(d.expiry)
}

// forget drops token if it is still the cached one.
func (d *Directory) forget(token string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.token == token {
		d.token = ""
	}
}

// adminPassword reads the administrator's password from its Secret. It is
// read for each login and never kept.
func (d *Directory) adminPassword(ctx context.Context) (string, error) {
	obj, err := d.client.Get(ctx, adminSecretPath)
	if err != nil {
		return "", fmt.Errorf("admin secret: %w", err)
	}
	var data map[string][]byte
	if err := json.Unmarshal(obj.Data, &data); err != nil {
		return "", fmt.Errorf("admin secret: %w", err)
	}
	pw := string(data[AdminSecretKey])
	if pw == "" {
		return "", fmt.Errorf("admin secret: no %q key", AdminSecretKey)
	}
	return pw, nil
}

// add appends e to res unless res is full, in which case it marks res
// truncated and reports false, ending the search.
func add(res *identity.DirectoryResult, e identity.DirectoryEntry) bool {
	if len(res.Items) == maxResults {
		res.Truncated = true
		return false
	}
	res.Items = append(res.Items, e)
	return true
}

// done reports whether a search has read everything it will: the last
// page, or maxScanned entries, which marks res truncated when more remain.
func done(res *identity.DirectoryResult, offset, n, total int) bool {
	switch {
	case lastPage(offset, n, total):
		return true
	case offset+n >= maxScanned:
		res.Truncated = true
		return true
	}
	return false
}

// lastPage reports whether a page of n entries at offset was the last. A
// response without totalResults is taken as complete only once a page
// comes back short.
func lastPage(offset, n, total int) bool {
	if total > 0 {
		return n == 0 || offset+n >= total
	}
	return n < pageSize
}

func containsFold(q string, fields ...string) bool {
	for _, f := range fields {
		if f != "" && strings.Contains(strings.ToLower(f), q) {
			return true
		}
	}
	return false
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
