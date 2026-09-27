package bootstrap

import (
	"os/exec"
	"testing"
)

func TestParseSystemdStatus(t *testing.T) {
	tests := []struct {
		name        string
		output      string
		cmdErr      error
		wantOK      bool
		wantWarning bool
		wantErr     bool
	}{
		{"running", "running\n", nil, true, false, false},
		{"degraded", "degraded\n", &exec.ExitError{}, true, true, false},
		{"offline", "offline\n", &exec.ExitError{}, false, false, true},
		{"empty with real command error", "", &exec.ExitError{}, false, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, warning, err := parseSystemdStatus([]byte(tt.output), tt.cmdErr)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if (warning != "") != tt.wantWarning {
				t.Errorf("warning = %q, want non-empty = %v", warning, tt.wantWarning)
			}
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, want non-nil = %v", err, tt.wantErr)
			}
		})
	}
}

func TestParseSystemdStatus_SystemctlNotFound(t *testing.T) {
	notFoundErr := &exec.Error{Name: "systemctl", Err: exec.ErrNotFound}
	ok, _, err := parseSystemdStatus(nil, notFoundErr)
	if ok {
		t.Error("ok = true, want false when systemctl is not found")
	}
	if err == nil {
		t.Fatal("err = nil, want a clear \"systemd not present\" error")
	}
}

func TestParseIPTablesVersion(t *testing.T) {
	tests := []struct {
		name        string
		output      string
		wantVersion string
		wantMode    string
		wantErr     bool
	}{
		{"nf_tables mode", "iptables v1.8.9 (nf_tables)\n", "1.8.9", "nf_tables", false},
		{"legacy mode", "iptables v1.8.9 (legacy)\n", "1.8.9", "legacy", false},
		{"risky nf_tables version", "iptables v1.8.4 (nf_tables)\n", "1.8.4", "nf_tables", false},
		{"unrecognized format", "some unexpected output\n", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version, mode, err := parseIPTablesVersion([]byte(tt.output))
			if tt.wantErr {
				if err == nil {
					t.Fatal("err = nil, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseIPTablesVersion() = %v", err)
			}
			if version != tt.wantVersion || mode != tt.wantMode {
				t.Errorf("parseIPTablesVersion() = (%q, %q), want (%q, %q)", version, mode, tt.wantVersion, tt.wantMode)
			}
		})
	}
}

func TestIsRiskyIPTablesVersion(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{"1.8.0", true},
		{"1.8.4", true},
		{"1.8.5", false},
		{"1.8.9", false},
		{"1.6.0", false},
		{"2.0.0", false},
		{"not-a-version", false},
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			if got := isRiskyIPTablesVersion(tt.version); got != tt.want {
				t.Errorf("isRiskyIPTablesVersion(%q) = %v, want %v", tt.version, got, tt.want)
			}
		})
	}
}
