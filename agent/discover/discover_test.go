package discover

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"
)

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("ReadFile(testdata/%s) = %v", name, err)
	}
	return b
}

func TestParseMemTotal(t *testing.T) {
	got, err := parseMemTotal(readTestdata(t, "meminfo_normal.txt"))
	if err != nil {
		t.Fatalf("parseMemTotal() = %v", err)
	}
	want := int64(16336880) * 1024
	if got != want {
		t.Errorf("parseMemTotal() = %d, want %d", got, want)
	}
}

func TestParseMemTotal_Malformed(t *testing.T) {
	_, err := parseMemTotal([]byte("nothing relevant here\n"))
	if err == nil {
		t.Fatal("parseMemTotal(no MemTotal line) = nil error, want an error")
	}
}

func TestParseVirtualizationSupport(t *testing.T) {
	tests := []struct {
		name     string
		fixture  string
		wantSupp bool
	}{
		{"vmx present", "cpuinfo_vmx.txt", true},
		{"no virtualization flags", "cpuinfo_no_virt.txt", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseVirtualizationSupport(readTestdata(t, tt.fixture))
			if err != nil {
				t.Fatalf("parseVirtualizationSupport() = %v", err)
			}
			if got != tt.wantSupp {
				t.Errorf("parseVirtualizationSupport() = %v, want %v", got, tt.wantSupp)
			}
		})
	}
}

func TestParseVirtualizationSupport_NoFlagsLine(t *testing.T) {
	_, err := parseVirtualizationSupport([]byte("processor : 0\nvendor_id : GenuineIntel\n"))
	if err == nil {
		t.Fatal("parseVirtualizationSupport(no flags line) = nil error, want an error")
	}
}

func TestRootBlockDevice(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		want    string
	}{
		{"traditional SATA naming", "mounts_traditional.txt", "sda"},
		{"NVMe naming", "mounts_nvme.txt", "nvme0n1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := rootBlockDevice(readTestdata(t, tt.fixture))
			if err != nil {
				t.Fatalf("rootBlockDevice() = %v", err)
			}
			if got != tt.want {
				t.Errorf("rootBlockDevice() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRootBlockDevice_NoRootMount(t *testing.T) {
	_, err := rootBlockDevice([]byte("sysfs /sys sysfs rw 0 0\nproc /proc proc rw 0 0\n"))
	if err == nil {
		t.Fatal("rootBlockDevice(no / mount) = nil error, want an error")
	}
}

func TestStripPartitionSuffix(t *testing.T) {
	tests := []struct {
		device string
		want   string
	}{
		{"sda2", "sda"},
		{"sda1", "sda"},
		{"vda1", "vda"},
		{"xvda1", "xvda"},
		{"nvme0n1p2", "nvme0n1"},
		{"nvme0n1p1", "nvme0n1"},
		{"nvme1n1p3", "nvme1n1"},
		{"mmcblk0p1", "mmcblk0"},
		{"nvme0n1", "nvme0n1"}, // unpartitioned whole-disk device, unchanged
		{"sda", "sda"},         // unpartitioned whole-disk device, unchanged
	}
	for _, tt := range tests {
		t.Run(tt.device, func(t *testing.T) {
			if got := stripPartitionSuffix(tt.device); got != tt.want {
				t.Errorf("stripPartitionSuffix(%q) = %q, want %q", tt.device, got, tt.want)
			}
		})
	}
}

// TestDiscover_ReturnsPlausibleResultOnLinux is T003's own Verify line's
// automated portion ("doesn't panic / returns a Result"); real-value
// correctness against actual hardware is T008's job in WSL2, which this
// repo's dev-machine `go test` run can't portably assert.
func TestDiscover_ReturnsPlausibleResultOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Discover only supports linux; real-value verification happens in WSL2 (T008)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := Discover(ctx)
	if err != nil {
		t.Fatalf("Discover() = %v", err)
	}
	if res.OS != "linux" {
		t.Errorf("OS = %q, want %q", res.OS, "linux")
	}
	if res.CPUCores <= 0 {
		t.Errorf("CPUCores = %d, want > 0", res.CPUCores)
	}
	if res.MemoryBytes <= 0 {
		t.Errorf("MemoryBytes = %d, want > 0", res.MemoryBytes)
	}
}

// TestDiscover_UnsupportedOS proves spec.md's Scope-OUT: a non-Linux
// host gets a clear error, not a partially-populated Result from
// haphazard file-read failures.
func TestDiscover_UnsupportedOS(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("this test targets the non-linux rejection path")
	}
	_, err := Discover(context.Background())
	if err == nil {
		t.Fatal("Discover() on non-linux = nil error, want an error")
	}
}

func TestParseDiskStats(t *testing.T) {
	tests := []struct {
		name          string
		sizeStr       string
		rotationalStr string
		deviceName    string
		wantBytes     int64
		wantType      string
		wantErr       bool
	}{
		{
			name:          "SSD (rotational=0)",
			sizeStr:       "500118192",
			rotationalStr: "0",
			deviceName:    "sda",
			wantBytes:     500118192 * 512,
			wantType:      "ssd",
		},
		{
			name:          "HDD (rotational=1)",
			sizeStr:       "1953525168",
			rotationalStr: "1",
			deviceName:    "sda",
			wantBytes:     1953525168 * 512,
			wantType:      "hdd",
		},
		{
			name:          "NVMe (device-name prefix wins regardless of rotational)",
			sizeStr:       "1000215216",
			rotationalStr: "0",
			deviceName:    "nvme0n1",
			wantBytes:     1000215216 * 512,
			wantType:      "nvme",
		},
		{
			name:          "unrecognized rotational value",
			sizeStr:       "1000",
			rotationalStr: "weird",
			deviceName:    "sda",
			wantErr:       true,
		},
		{
			name:          "malformed size",
			sizeStr:       "not-a-number",
			rotationalStr: "0",
			deviceName:    "sda",
			wantErr:       true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotBytes, gotType, err := parseDiskStats(tt.sizeStr, tt.rotationalStr, tt.deviceName)
			if tt.wantErr {
				if err == nil {
					t.Fatal("parseDiskStats() = nil error, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseDiskStats() = %v", err)
			}
			if gotBytes != tt.wantBytes {
				t.Errorf("parseDiskStats() bytes = %d, want %d", gotBytes, tt.wantBytes)
			}
			if gotType != tt.wantType {
				t.Errorf("parseDiskStats() type = %q, want %q", gotType, tt.wantType)
			}
		})
	}
}
