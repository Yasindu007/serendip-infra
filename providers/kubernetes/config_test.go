package kubernetes

import (
	"os"
	"testing"
)

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("ReadFile(testdata/%s) = %v", name, err)
	}
	return b
}

// TestParseConfig_ClientCert mirrors K3s's own generated
// /etc/rancher/k3s/k3s.yaml shape exactly, confirmed against K3s's
// source (pkg/clientaccess/kubeconfig.go) rather than assumed.
func TestParseConfig_ClientCert(t *testing.T) {
	cfg, err := ParseConfig(readTestdata(t, "kubeconfig-client-cert.yaml"))
	if err != nil {
		t.Fatalf("ParseConfig() = %v", err)
	}
	if cfg.Host != "https://127.0.0.1:6443" {
		t.Errorf("Host = %q, want %q", cfg.Host, "https://127.0.0.1:6443")
	}
	if string(cfg.CAData) != "fake-ca-certificate-data-for-testing" {
		t.Errorf("CAData = %q, want decoded fixture value", cfg.CAData)
	}
	if string(cfg.ClientCertData) != "fake-client-certificate-data-for-testing" {
		t.Errorf("ClientCertData = %q, want decoded fixture value", cfg.ClientCertData)
	}
	if string(cfg.ClientKeyData) != "fake-client-key-data-for-testing" {
		t.Errorf("ClientKeyData = %q, want decoded fixture value", cfg.ClientKeyData)
	}
	if cfg.BearerToken != "" {
		t.Errorf("BearerToken = %q, want empty for a client-cert kubeconfig", cfg.BearerToken)
	}
}

// TestParseConfig_BearerToken proves the parser isn't hardcoded to
// K3s's client-cert style -- a second, differently-shaped real auth
// method parses correctly too.
func TestParseConfig_BearerToken(t *testing.T) {
	cfg, err := ParseConfig(readTestdata(t, "kubeconfig-bearer-token.yaml"))
	if err != nil {
		t.Fatalf("ParseConfig() = %v", err)
	}
	if cfg.Host != "https://example.invalid:6443" {
		t.Errorf("Host = %q, want %q", cfg.Host, "https://example.invalid:6443")
	}
	if cfg.BearerToken != "fake-bearer-token-for-testing" {
		t.Errorf("BearerToken = %q, want %q", cfg.BearerToken, "fake-bearer-token-for-testing")
	}
	if cfg.ClientCertData != nil || cfg.ClientKeyData != nil {
		t.Errorf("ClientCertData/ClientKeyData = %q/%q, want nil for a bearer-token kubeconfig", cfg.ClientCertData, cfg.ClientKeyData)
	}
}

func TestParseConfig_MissingCurrentContext(t *testing.T) {
	_, err := ParseConfig([]byte(`
apiVersion: v1
kind: Config
clusters: []
contexts: []
users: []
`))
	if err == nil {
		t.Fatal("ParseConfig(no current-context) = nil error, want an error")
	}
}

func TestParseConfig_CurrentContextNotFound(t *testing.T) {
	_, err := ParseConfig([]byte(`
apiVersion: v1
kind: Config
current-context: does-not-exist
clusters: []
contexts: []
users: []
`))
	if err == nil {
		t.Fatal("ParseConfig(unresolvable current-context) = nil error, want an error")
	}
}

func TestParseConfig_ContextReferencesMissingCluster(t *testing.T) {
	_, err := ParseConfig([]byte(`
apiVersion: v1
kind: Config
current-context: ctx1
clusters: []
contexts:
- name: ctx1
  context:
    cluster: does-not-exist
    user: u1
users:
- name: u1
  user:
    token: t
`))
	if err == nil {
		t.Fatal("ParseConfig(context references missing cluster) = nil error, want an error")
	}
}

func TestParseConfig_UserHasNoRecognizedAuth(t *testing.T) {
	_, err := ParseConfig([]byte(`
apiVersion: v1
kind: Config
current-context: ctx1
clusters:
- name: c1
  cluster:
    server: https://example.invalid
contexts:
- name: ctx1
  context:
    cluster: c1
    user: u1
users:
- name: u1
  user: {}
`))
	if err == nil {
		t.Fatal("ParseConfig(user with no token or client cert/key) = nil error, want an error")
	}
}

func TestParseConfig_MalformedYAML(t *testing.T) {
	_, err := ParseConfig([]byte("not: valid: yaml: at: all: [[[")) //nolint:all -- deliberately malformed
	if err == nil {
		t.Fatal("ParseConfig(malformed YAML) = nil error, want an error")
	}
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := LoadConfig("/nonexistent/path/kubeconfig.yaml")
	if err == nil {
		t.Fatal("LoadConfig(nonexistent path) = nil error, want an error")
	}
}
