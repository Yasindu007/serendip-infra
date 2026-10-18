package main

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseAgentIssuers(t *testing.T) {
	const human = "http://identity.localhost:30080"
	tests := []struct {
		raw     string
		want    []agentIssuer
		wantErr string
	}{
		{raw: "", want: nil},
		{
			raw:  "am-dev=http://default-idp.amp.localhost:8080|https://serendip.local/api",
			want: []agentIssuer{{alias: "am-dev", issuer: "http://default-idp.amp.localhost:8080", audience: "https://serendip.local/api"}},
		},
		{
			raw: " a=https://a.example|aud-a|30m , b=https://b.example/|aud-b ",
			want: []agentIssuer{
				{alias: "a", issuer: "https://a.example", audience: "aud-a", maxLifetime: 30 * time.Minute},
				{alias: "b", issuer: "https://b.example/", audience: "aud-b"},
			},
		},
		{raw: "am-dev", wantErr: "want alias="},
		{raw: "am-dev=https://a.example", wantErr: "want alias="},
		{raw: "am-dev=https://a.example|aud|1h|extra", wantErr: "want alias="},
		{raw: "AM=https://a.example|aud", wantErr: "alias"},
		{raw: "a/b=https://a.example|aud", wantErr: "alias"},
		{raw: "a=ftp://a.example|aud", wantErr: "absolute http(s)"},
		{raw: "a=https://a.example?x=1|aud", wantErr: "absolute http(s)"},
		{raw: "a=https://a.example|", wantErr: "audience"},
		{raw: "a=https://a.example|aud|25h", wantErr: "maxLifetime"},
		{raw: "a=https://a.example|aud|-1h", wantErr: "maxLifetime"},
		{raw: "a=https://a.example|aud,a=https://b.example|aud", wantErr: "used twice"},
		{raw: "a=https://a.example|aud,b=https://a.example|aud", wantErr: "listed twice"},
		{raw: "a=https://a.example|aud,b=https://a.example/|aud", wantErr: "listed twice"},
		{raw: "a=" + human + "/|aud", wantErr: "human identity issuer"},
	}
	for _, tt := range tests {
		got, err := parseAgentIssuers(tt.raw, human)
		if tt.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("parseAgentIssuers(%q) err = %v, want it to mention %q", tt.raw, err, tt.wantErr)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("parseAgentIssuers(%q) = %+v, %v; want %+v", tt.raw, got, err, tt.want)
		}
	}
}
