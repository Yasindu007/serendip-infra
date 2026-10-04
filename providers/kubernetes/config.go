package kubernetes

import (
	"encoding/base64"
	"fmt"
	"os"

	"sigs.k8s.io/yaml"
)

// Config is the flat, TLS-ready result of resolving a kubeconfig's
// current context. Exactly one of (ClientCertData, ClientKeyData) or
// BearerToken is populated, matching what a real kubeconfig actually
// provides for one auth method -- K3s's own generated kubeconfig uses
// client certs, not a bearer token (confirmed against K3s's source,
// pkg/clientaccess/kubeconfig.go), but this parser supports both shapes
// since nothing about the kubeconfig format itself is K3s-specific.
type Config struct {
	Host           string
	CAData         []byte
	ClientCertData []byte
	ClientKeyData  []byte
	BearerToken    string
}

// kubeconfigFile mirrors the on-disk YAML shape -- client-go's
// clientcmdapi.Config marshals clusters/contexts/users as YAML LISTS of
// {name, <type>} objects, not maps keyed by name (confirmed against
// K3s's own generated output), so these types must too.
type kubeconfigFile struct {
	APIVersion     string          `json:"apiVersion"`
	Kind           string          `json:"kind"`
	CurrentContext string          `json:"current-context"`
	Clusters       []namedCluster  `json:"clusters"`
	Contexts       []namedContext  `json:"contexts"`
	Users          []namedAuthInfo `json:"users"`
}

type namedCluster struct {
	Name    string  `json:"name"`
	Cluster cluster `json:"cluster"`
}

type cluster struct {
	Server                   string `json:"server"`
	CertificateAuthorityData string `json:"certificate-authority-data,omitempty"`
}

type namedContext struct {
	Name    string  `json:"name"`
	Context context `json:"context"`
}

type context struct {
	Cluster string `json:"cluster"`
	User    string `json:"user"`
}

type namedAuthInfo struct {
	Name string   `json:"name"`
	User authInfo `json:"user"`
}

type authInfo struct {
	ClientCertificateData string `json:"client-certificate-data,omitempty"`
	ClientKeyData         string `json:"client-key-data,omitempty"`
	Token                 string `json:"token,omitempty"`
}

// LoadConfig reads and parses the kubeconfig file at path, resolving its
// current-context into a flat Config. Multi-context kubeconfigs parse
// fine (current-context selects which one); this package only ever
// needs the one context a real installer (K3s's) produces.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("kubernetes: read kubeconfig %s: %w", path, err)
	}
	return ParseConfig(data)
}

// ParseConfig parses already-read kubeconfig YAML content -- separated
// from LoadConfig so parsing logic is testable against fixture content
// without real file I/O, same pattern as agent/discover's parse
// functions.
func ParseConfig(data []byte) (Config, error) {
	var kc kubeconfigFile
	if err := yaml.Unmarshal(data, &kc); err != nil {
		return Config{}, fmt.Errorf("kubernetes: parse kubeconfig: %w", err)
	}
	if kc.CurrentContext == "" {
		return Config{}, fmt.Errorf("kubernetes: kubeconfig has no current-context set")
	}

	var ctx *context
	for _, c := range kc.Contexts {
		if c.Name == kc.CurrentContext {
			ctx = &c.Context
			break
		}
	}
	if ctx == nil {
		return Config{}, fmt.Errorf("kubernetes: current-context %q not found in contexts", kc.CurrentContext)
	}

	var cl *cluster
	for _, c := range kc.Clusters {
		if c.Name == ctx.Cluster {
			cl = &c.Cluster
			break
		}
	}
	if cl == nil {
		return Config{}, fmt.Errorf("kubernetes: context %q references cluster %q, not found in clusters", kc.CurrentContext, ctx.Cluster)
	}

	var user *authInfo
	for _, u := range kc.Users {
		if u.Name == ctx.User {
			user = &u.User
			break
		}
	}
	if user == nil {
		return Config{}, fmt.Errorf("kubernetes: context %q references user %q, not found in users", kc.CurrentContext, ctx.User)
	}

	caData, err := decodeBase64Field("certificate-authority-data", cl.CertificateAuthorityData)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{Host: cl.Server, CAData: caData}

	switch {
	case user.Token != "":
		cfg.BearerToken = user.Token
	case user.ClientCertificateData != "" && user.ClientKeyData != "":
		certData, err := decodeBase64Field("client-certificate-data", user.ClientCertificateData)
		if err != nil {
			return Config{}, err
		}
		keyData, err := decodeBase64Field("client-key-data", user.ClientKeyData)
		if err != nil {
			return Config{}, err
		}
		cfg.ClientCertData = certData
		cfg.ClientKeyData = keyData
	default:
		return Config{}, fmt.Errorf("kubernetes: user %q has neither a bearer token nor a client cert/key pair", ctx.User)
	}

	return cfg, nil
}

func decodeBase64Field(fieldName, value string) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: decode %s: %w", fieldName, err)
	}
	return decoded, nil
}
