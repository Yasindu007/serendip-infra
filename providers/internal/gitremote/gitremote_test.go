package gitremote

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// The bodies in testdata were recorded in T001 item 7: a public GitHub
// repository, a private one behind basic auth, and GitHub's answer for
// a repository without the right credential.
func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// server serves body as info/refs, requiring user:pass when user isn't
// empty, and records what was asked.
func server(t *testing.T, body []byte, user, pass string) (*httptest.Server, *http.Request) {
	t.Helper()
	var got http.Request
	denied := readTestdata(t, "github-401.txt")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = *r.Clone(context.Background())
		if u, p, ok := r.BasicAuth(); user != "" && (!ok || u != user || p != pass) {
			w.Header().Set("WWW-Authenticate", `Basic realm="GitHub"`)
			w.WriteHeader(http.StatusUnauthorized)
			w.Write(denied)
			return
		}
		w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func TestHeadCommit(t *testing.T) {
	public := readTestdata(t, "github-public.pkt")
	private := readTestdata(t, "private-basic.pkt")
	tests := []struct {
		name       string
		body       []byte
		user, pass string // what the server requires
		branch     string
		cred       *Credential
		want       string
		wantErr    error
	}{
		{name: "public main", body: public, branch: "main", want: "19a03d1880f84504fffa4d573a20e3b488f7a655"},
		{name: "public other branch", body: public, branch: "go-greeter", want: "6ebff91434760d6e9125ae53eba570ebe36c73ef"},
		{name: "a branch with a slash", body: public, branch: "fix/frontend-nginx-port", want: "40c8f5d3f486e79f6565b051c8ed6f9b3de23206"},
		{name: "a missing branch", body: public, branch: "nope", wantErr: ErrNoBranch},
		{name: "a branch name's prefix isn't a match", body: public, branch: "mai", wantErr: ErrNoBranch},
		{name: "private with the credential", body: private, user: "u", pass: "s3cret", branch: "main",
			cred: &Credential{Username: "u", Password: "s3cret"}, want: "b4fe321b6c900c7193ccf3bb3c09e129163fd026"},
		{name: "private without a credential", body: private, user: "u", pass: "s3cret", branch: "main", wantErr: ErrAuth},
		{name: "private with a wrong credential", body: private, user: "u", pass: "s3cret", branch: "main",
			cred: &Credential{Username: "u", Password: "wrong"}, wantErr: ErrAuth},
		{name: "private, missing branch", body: private, user: "u", pass: "s3cret", branch: "dev",
			cred: &Credential{Username: "u", Password: "s3cret"}, wantErr: ErrNoBranch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, req := server(t, tt.body, tt.user, tt.pass)
			got, err := HeadCommit(context.Background(), srv.Client(), srv.URL+"/org/repo.git", tt.branch, tt.cred)
			if !errors.Is(err, tt.wantErr) || got != tt.want {
				t.Fatalf("HeadCommit = %q, %v; want %q, %v", got, err, tt.want, tt.wantErr)
			}
			if req.URL.Path != "/org/repo.git/info/refs" || req.URL.RawQuery != "service=git-upload-pack" {
				t.Errorf("asked for %s", req.URL)
			}
			if !strings.HasPrefix(req.Header.Get("User-Agent"), "git/") {
				t.Errorf("User-Agent = %q", req.Header.Get("User-Agent"))
			}
		})
	}
}

func TestHeadCommitRefusesSSH(t *testing.T) {
	for _, repo := range []string{"ssh://git@github.com/org/repo.git", "git@github.com:org/repo.git"} {
		_, err := HeadCommit(context.Background(), nil, repo, "main", nil)
		if !errors.Is(err, ErrSSH) || !strings.Contains(err.Error(), "use HTTPS") {
			t.Errorf("%s: err = %v, want ErrSSH", repo, err)
		}
	}
}

func TestHeadCommitRefusesOtherURLs(t *testing.T) {
	for _, repo := range []string{"http://example.com/r.git", "https://u:s3cret@example.com/r.git", "https://example.com/r.git?x=1", "file:///tmp/r"} {
		_, err := HeadCommit(context.Background(), nil, repo, "main", nil)
		if err == nil {
			t.Errorf("%s: no error", repo)
		} else if strings.Contains(err.Error(), "s3cret") {
			t.Errorf("%s: the error repeats the credential: %v", repo, err)
		}
	}
}

func TestHeadCommitRefusesARedirectOffHTTPS(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := r.BasicAuth(); ok {
			t.Error("the credential was sent over plain HTTP")
		}
	}))
	defer plain.Close()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+r.URL.RequestURI(), http.StatusFound)
	}))
	defer srv.Close()
	_, err := HeadCommit(context.Background(), srv.Client(), srv.URL+"/r.git", "main", &Credential{Username: "u", Password: "p"})
	if err == nil || !strings.Contains(err.Error(), "away from HTTPS") {
		t.Fatalf("err = %v", err)
	}
}

func TestHeadCommitTrailingSlash(t *testing.T) {
	srv, req := server(t, readTestdata(t, "github-public.pkt"), "", "")
	if _, err := HeadCommit(context.Background(), srv.Client(), srv.URL+"/org/repo/", "main", nil); err != nil {
		t.Fatal(err)
	}
	if req.URL.Path != "/org/repo/info/refs" {
		t.Errorf("asked for %s", req.URL.Path)
	}
}

func TestHeadCommitNotSmartHTTP(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("19a03d1880f84504fffa4d573a20e3b488f7a655\trefs/heads/main\n"))
	}))
	defer srv.Close()
	_, err := HeadCommit(context.Background(), srv.Client(), srv.URL+"/r.git", "main", nil)
	if err == nil || !strings.Contains(err.Error(), "smart HTTP") {
		t.Fatalf("err = %v", err)
	}
}

func TestFindRefMalformed(t *testing.T) {
	tests := map[string]string{
		"bad length":     "zzzz",
		"short length":   "0003",
		"truncated":      "0040abc",
		"not a ref line": "000bhello\n",
		"server error":   "000eERR denied\n",
		"too long":       "fff1",
		"no final flush": "001e# service=git-upload-pack\n0000",
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := findRef(strings.NewReader(body), "refs/heads/main")
			if err == nil || errors.Is(err, ErrNoBranch) {
				t.Fatalf("err = %v, want a parse error", err)
			}
		})
	}
}

func TestFindRefEmptyRepository(t *testing.T) {
	// An empty repository advertises the zero ID with its capabilities.
	body := "001e# service=git-upload-pack\n0000" + pkt("0000000000000000000000000000000000000000 capabilities^{}\x00agent=git/2\n") + "0000"
	if _, err := findRef(strings.NewReader(body), "refs/heads/main"); !errors.Is(err, ErrNoBranch) {
		t.Fatalf("err = %v, want ErrNoBranch", err)
	}
}

func pkt(s string) string { return fmt.Sprintf("%04x%s", len(s)+4, s) }

func TestFindRefCapIsSaid(t *testing.T) {
	var b strings.Builder
	b.WriteString("001e# service=git-upload-pack\n0000")
	for i := 0; b.Len() < maxAdvertisement+1000; i++ {
		b.WriteString(pkt(fmt.Sprintf("19a03d1880f84504fffa4d573a20e3b488f7a655 refs/tags/v%d\n", i)))
	}
	r := &cappedReader{r: strings.NewReader(b.String()), left: maxAdvertisement}
	if _, err := findRef(r, "refs/heads/main"); !errors.Is(err, errTooLarge) {
		t.Fatalf("err = %v, want errTooLarge", err)
	}
}

func TestHeadCommitStatuses(t *testing.T) {
	body := readTestdata(t, "github-public.pkt")
	tests := []struct {
		status  int
		ct      string
		wantErr error
		wantMsg string
	}{
		{http.StatusNotFound, "", ErrNotFound, ""},
		{http.StatusForbidden, "", ErrAuth, ""},
		{http.StatusBadGateway, "", nil, "502"},
		{http.StatusOK, "application/x-git-upload-pack-advertisement; charset=utf-8", nil, ""},
	}
	for _, tt := range tests {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if tt.ct != "" {
				w.Header().Set("Content-Type", tt.ct)
			}
			w.WriteHeader(tt.status)
			w.Write(body)
		}))
		got, err := HeadCommit(context.Background(), srv.Client(), srv.URL+"/r.git", "main", nil)
		srv.Close()
		switch {
		case tt.wantErr != nil && !errors.Is(err, tt.wantErr):
			t.Errorf("%d: err = %v, want %v", tt.status, err, tt.wantErr)
		case tt.wantMsg != "" && (err == nil || !strings.Contains(err.Error(), tt.wantMsg)):
			t.Errorf("%d: err = %v, want %q", tt.status, err, tt.wantMsg)
		case tt.wantErr == nil && tt.wantMsg == "" && (err != nil || got == ""):
			t.Errorf("%d: %q, %v", tt.status, got, err)
		}
	}
}

func TestHeadCommitCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	srv, _ := server(t, nil, "", "")
	if _, err := HeadCommit(ctx, srv.Client(), srv.URL+"/r.git", "main", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
