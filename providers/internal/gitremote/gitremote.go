// Package gitremote reads a branch's head commit from a Git repository
// over the smart HTTP protocol (specs/022-builds plan decision 7): one
// GET of info/refs, parsed as pkt-lines, protocol v0. It needs no Git
// binary and no dependency beyond the standard library, and it never
// downloads objects.
package gitremote

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var (
	// ErrSSH is returned for an SSH URL, which can't be read over HTTP.
	ErrSSH = errors.New("can't see new commits for SSH URLs; use HTTPS")
	// ErrAuth means the server asked for a credential, or refused the one
	// given. GitHub answers this way for a repository that doesn't exist
	// too (T001 item 7), so the two can't be told apart.
	ErrAuth = errors.New("the repository needs a credential, or doesn't exist")
	// ErrNoBranch means the repository has no such branch.
	ErrNoBranch = errors.New("the repository has no such branch")
	// ErrNotFound means the server says there's no such repository.
	ErrNotFound = errors.New("the repository doesn't exist")
)

// Credential is HTTP basic auth: a user name and a password or token.
type Credential struct {
	Username string
	Password string
}

// userAgent starts with "git/", as some hosts only speak the smart
// protocol to Git clients (T001 item 7).
const userAgent = "git/serendip"

// maxAdvertisement caps how much of the ref advertisement is read before
// giving up on finding the branch: a repository with very many tags
// shouldn't cost serendipd more memory or time than this.
const maxAdvertisement = 8 << 20

// maxPkt is the largest pkt-line the protocol allows (65520 bytes).
const maxPkt = 65520

var commitRE = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// HeadCommit returns the full commit ID at refs/heads/<branch> in the
// repository at repo, an https:// URL. cred may be nil for a public
// repository. client may be nil for http.DefaultClient; its timeout, or
// ctx, bounds the request.
func HeadCommit(ctx context.Context, client *http.Client, repo, branch string, cred *Credential) (string, error) {
	if strings.HasPrefix(repo, "ssh://") || (!strings.Contains(repo, "://") && strings.Contains(repo, "@")) {
		return "", ErrSSH
	}
	u, err := url.Parse(repo)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		// The URL isn't repeated: it may carry a credential.
		return "", errors.New("gitremote: not an https:// repository URL without credentials, a query or a fragment")
	}
	if client == nil {
		client = http.DefaultClient
	}
	c := *client
	c.CheckRedirect = httpsOnly
	client = &c
	u.Path = strings.TrimSuffix(u.Path, "/") + "/info/refs"
	u.RawPath = ""
	u.RawQuery = "service=git-upload-pack"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", fmt.Errorf("gitremote: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/x-git-upload-pack-advertisement")
	if cred != nil {
		req.SetBasicAuth(cred.Username, cred.Password)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("gitremote: %w", err)
	}
	defer func() {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10)) // so the connection can be reused
		resp.Body.Close()
	}()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return "", ErrAuth
	case resp.StatusCode == http.StatusNotFound:
		return "", ErrNotFound
	case resp.StatusCode != http.StatusOK:
		return "", fmt.Errorf("gitremote: the repository answered %s", resp.Status)
	}
	ct := resp.Header.Get("Content-Type")
	if mt, _, err := mime.ParseMediaType(ct); err != nil || mt != "application/x-git-upload-pack-advertisement" {
		return "", fmt.Errorf("gitremote: the server doesn't speak Git's smart HTTP protocol (Content-Type %q)", ct)
	}
	return findRef(&cappedReader{r: resp.Body, left: maxAdvertisement}, "refs/heads/"+branch)
}

// httpsOnly refuses a redirect off HTTPS: Go keeps the credential on a
// redirect to the same host, even over plain HTTP.
func httpsOnly(req *http.Request, via []*http.Request) error {
	if req.URL.Scheme != "https" {
		return errors.New("gitremote: the repository redirected away from HTTPS")
	}
	if len(via) >= 10 {
		return errors.New("gitremote: too many redirects")
	}
	return nil
}

// errTooLarge is the cap on the advertisement being reached.
var errTooLarge = fmt.Errorf("gitremote: the repository advertises more than %d MiB of refs before the branch", maxAdvertisement>>20)

// cappedReader is io.LimitReader with its own error once the cap is
// reached, so a cut-off advertisement isn't read as a missing branch.
type cappedReader struct {
	r    io.Reader
	left int64
}

func (c *cappedReader) Read(p []byte) (int, error) {
	if c.left <= 0 {
		return 0, errTooLarge
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	return n, err
}

// findRef reads a protocol v0 ref advertisement, as served by info/refs,
// until it finds ref, and returns ref's object ID. The refs end with a
// flush; input that ends before it is truncated.
func findRef(r io.Reader, ref string) (string, error) {
	br := bufio.NewReader(r)
	first, refs := true, false
	for {
		line, flush, err := readPkt(br)
		if err != nil {
			return "", err
		}
		if flush {
			if refs {
				return "", ErrNoBranch // the end of the refs
			}
			continue // after the service announcement
		}
		if first {
			first = false
			if strings.HasPrefix(line, "# service=") {
				continue
			}
		}
		if msg, ok := strings.CutPrefix(line, "ERR "); ok {
			return "", fmt.Errorf("gitremote: the server said: %s", clip(msg))
		}
		refs = true
		// "<oid> <ref>", the first ref with "\x00<capabilities>" after it.
		line, _, _ = strings.Cut(line, "\x00")
		oid, name, ok := strings.Cut(line, " ")
		if !ok || !commitRE.MatchString(oid) {
			return "", fmt.Errorf("gitremote: a malformed ref line %q", clip(line))
		}
		if name == ref {
			return oid, nil
		}
	}
}

// clip shortens server text quoted in an error.
func clip(s string) string {
	if len(s) > 200 {
		return strings.ToValidUTF8(s[:200], "") + "..."
	}
	return s
}

// readPkt reads one pkt-line: a four-hex-digit length that counts
// itself, then the payload, whose trailing newline is dropped. "0000" is
// a flush.
func readPkt(r *bufio.Reader) (line string, flush bool, err error) {
	var head [4]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return "", false, truncated(err)
	}
	n, err := strconv.ParseUint(string(head[:]), 16, 16)
	if err != nil {
		return "", false, fmt.Errorf("gitremote: not a pkt-line length %q", head[:])
	}
	switch {
	case n == 0:
		return "", true, nil
	case n < 4 || n > maxPkt:
		return "", false, fmt.Errorf("gitremote: a pkt-line length of %d", n)
	}
	payload := make([]byte, n-4)
	if _, err := io.ReadFull(r, payload); err != nil {
		return "", false, truncated(err)
	}
	return strings.TrimSuffix(string(payload), "\n"), false, nil
}

func truncated(err error) error {
	if errors.Is(err, errTooLarge) {
		return errTooLarge
	}
	return fmt.Errorf("gitremote: a truncated ref advertisement: %w", err)
}
