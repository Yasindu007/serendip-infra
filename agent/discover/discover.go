package discover

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Result is everything one Discover call reports. The first six fields
// map one-to-one onto core/resource.NodeStatus's hardware facts
// (agent/client owns that mapping, not this package -- agent/ stays
// decoupled from core/resource per specs/004-bootstrap-agent/plan.md's
// resolved wire-format decision). The remaining environment facts have
// no corresponding NodeStatus fields yet (resource-model.md's Network
// resource is still unresolved) -- Discover still reports them (printed
// by the `discover` CLI subcommand for a human to see), they're just
// not sent anywhere by `register` today.
type Result struct {
	CPUCores              int
	MemoryBytes           int64
	DiskType              string // "nvme", "ssd", "hdd", or "" if undetermined
	DiskBytes             int64
	NetworkMbps           int // highest observed among up, non-loopback interfaces; 0 if none determinable
	VirtualizationSupport bool

	OS                string
	IPAddresses       []string
	DNSServers        []string
	InternetReachable bool
}

// Discover reads hardware and environment facts from the local host.
// Linux-only (specs/004-bootstrap-agent/spec.md Scope-OUT: "discovery on
// any other OS returns a clear error, not a supported code path") --
// checked up front rather than letting individual file reads fail
// haphazardly partway through.
func Discover(ctx context.Context) (Result, error) {
	if runtime.GOOS != "linux" {
		return Result{}, fmt.Errorf("discover: unsupported OS %q, only linux is supported", runtime.GOOS)
	}

	var res Result
	res.OS = runtime.GOOS
	res.CPUCores = runtime.NumCPU()

	meminfo, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return Result{}, fmt.Errorf("discover: read /proc/meminfo: %w", err)
	}
	if res.MemoryBytes, err = parseMemTotal(meminfo); err != nil {
		return Result{}, err
	}

	cpuinfo, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return Result{}, fmt.Errorf("discover: read /proc/cpuinfo: %w", err)
	}
	if res.VirtualizationSupport, err = parseVirtualizationSupport(cpuinfo); err != nil {
		return Result{}, err
	}

	mounts, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return Result{}, fmt.Errorf("discover: read /proc/mounts: %w", err)
	}
	device, err := rootBlockDevice(mounts)
	if err != nil {
		return Result{}, err
	}
	sizeStr, err := os.ReadFile(fmt.Sprintf("/sys/block/%s/size", device))
	if err != nil {
		return Result{}, fmt.Errorf("discover: read /sys/block/%s/size: %w", device, err)
	}
	rotationalStr, err := os.ReadFile(fmt.Sprintf("/sys/block/%s/queue/rotational", device))
	if err != nil {
		return Result{}, fmt.Errorf("discover: read /sys/block/%s/queue/rotational: %w", device, err)
	}
	if res.DiskBytes, res.DiskType, err = parseDiskStats(string(sizeStr), string(rotationalStr), device); err != nil {
		return Result{}, err
	}

	res.NetworkMbps = discoverNetworkMbps()
	res.IPAddresses = discoverIPAddresses()
	res.DNSServers = discoverDNSServers()
	res.InternetReachable = discoverInternetReachable(ctx)

	return res, nil
}

// discoverNetworkMbps reads /sys/class/net/<iface>/speed for every up,
// non-loopback interface and returns the highest value found. Best-
// effort, not an error path: many virtual/Wi-Fi/containerized
// interfaces -- including WSL2's own virtual adapter, which is what
// this spec's own verification runs against (plan.md Risks) -- don't
// expose a readable speed file, or report the kernel's "-1 = unknown"
// sentinel. Those are skipped, not treated as failures.
func discoverNetworkMbps() int {
	ifaces, err := net.Interfaces()
	if err != nil {
		return 0
	}
	best := 0
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		raw, err := os.ReadFile(fmt.Sprintf("/sys/class/net/%s/speed", iface.Name))
		if err != nil {
			continue
		}
		mbps, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil || mbps <= 0 {
			continue
		}
		if mbps > best {
			best = mbps
		}
	}
	return best
}

// discoverIPAddresses collects addresses from every up, non-loopback
// interface. Best-effort: an interface whose Addrs() call fails is
// skipped, not fatal to the whole Discover call.
func discoverIPAddresses() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var ips []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ips = append(ips, ipNet.IP.String())
		}
	}
	return ips
}

var nameserverRE = regexp.MustCompile(`^nameserver\s+(\S+)`)

// discoverDNSServers parses /etc/resolv.conf. Best-effort: a missing or
// unreadable file (some container/VM setups generate this dynamically
// or symlink it elsewhere) yields an empty result, not an error.
func discoverDNSServers() []string {
	data, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return nil
	}
	var servers []string
	for line := range strings.SplitSeq(string(data), "\n") {
		if m := nameserverRE.FindStringSubmatch(line); m != nil {
			servers = append(servers, m[1])
		}
	}
	return servers
}

// discoverInternetReachable does one bounded outbound TCP dial -- a
// reachability probe, not a hard dependency on the target staying up.
// Dials an IP directly (not a hostname) so a DNS outage doesn't get
// mistaken for a connectivity outage.
func discoverInternetReachable(ctx context.Context) bool {
	dialCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(dialCtx, "tcp", "1.1.1.1:443")
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// parseMemTotal extracts MemTotal from /proc/meminfo content, returning
// bytes. /proc/meminfo reports "kB" but the value is actually kibibytes
// (1024 bytes) -- a long-standing, never-fixed naming quirk in the
// kernel interface, not a bug in this parser.
func parseMemTotal(meminfo []byte) (int64, error) {
	scanner := bufio.NewScanner(bytes.NewReader(meminfo))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, fmt.Errorf("discover: malformed MemTotal line: %q", line)
		}
		kib, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("discover: parse MemTotal value %q: %w", fields[1], err)
		}
		return kib * 1024, nil
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("discover: scan meminfo: %w", err)
	}
	return 0, fmt.Errorf("discover: MemTotal not found in meminfo")
}

// parseVirtualizationSupport reports whether /proc/cpuinfo's "flags"
// line(s) include Intel VT-x ("vmx") or AMD-V ("svm") -- present on any
// core means hardware virtualization is available. x86-specific, which
// matches this spec's reference hardware (ThinkPad T470, Intel).
func parseVirtualizationSupport(cpuinfo []byte) (bool, error) {
	scanner := bufio.NewScanner(bytes.NewReader(cpuinfo))
	sawFlagsLine := false
	found := false
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "flags") {
			continue
		}
		sawFlagsLine = true
		for f := range strings.FieldsSeq(line) {
			if f == "vmx" || f == "svm" {
				found = true
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("discover: scan cpuinfo: %w", err)
	}
	if !sawFlagsLine {
		return false, fmt.Errorf("discover: no \"flags\" line found in cpuinfo")
	}
	return found, nil
}

// partitionSuffixRE matches NVMe ("nvme0n1p2") and eMMC/SD
// ("mmcblk0p1") partition naming, where the whole-disk device name
// itself ends in a digit -- a plain trailing-digit strip would wrongly
// turn "nvme0n1" into "nvme0n".
var partitionSuffixRE = regexp.MustCompile(`^(nvme\d+n\d+|mmcblk\d+)p\d+$`)

// trailingDigitsRE matches traditional SATA/virtio/Xen naming
// ("sda2" -> "sda", "vda1" -> "vda").
var trailingDigitsRE = regexp.MustCompile(`^([a-z]+)\d+$`)

// stripPartitionSuffix returns device's underlying whole-disk name, or
// device unchanged if it doesn't match either known naming scheme.
func stripPartitionSuffix(device string) string {
	if m := partitionSuffixRE.FindStringSubmatch(device); m != nil {
		return m[1]
	}
	if m := trailingDigitsRE.FindStringSubmatch(device); m != nil {
		return m[1]
	}
	return device
}

// rootBlockDevice finds the block device backing the root filesystem
// from /proc/mounts content and returns its underlying whole-disk name
// (e.g. "sda", "nvme0n1") -- the name /sys/block/<name> expects, not a
// partition name. specs/004-bootstrap-agent/plan.md's resolved decision:
// "the disk backing /" is the one disk this spec reports, not
// largest-or-summed.
func rootBlockDevice(mounts []byte) (string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(mounts))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		device, mountPoint := fields[0], fields[1]
		if mountPoint != "/" {
			continue
		}
		device = strings.TrimPrefix(device, "/dev/")
		return stripPartitionSuffix(device), nil
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("discover: scan mounts: %w", err)
	}
	return "", fmt.Errorf("discover: no mount entry for /")
}

// parseDiskStats interprets /sys/block/<device>/size (always reported in
// 512-byte sectors, regardless of the disk's actual physical sector
// size) and .../queue/rotational content for deviceName. diskType is a
// reported value, not a fixed enum (core/resource.NodeStatus.DiskType's
// own doc comment): "nvme" by device name prefix (NVMe is never
// rotational, so its own rotational file is redundant to check), "hdd"
// for rotational=1, "ssd" for rotational=0 on anything else.
func parseDiskStats(sizeStr, rotationalStr, deviceName string) (int64, string, error) {
	sectors, err := strconv.ParseInt(strings.TrimSpace(sizeStr), 10, 64)
	if err != nil {
		return 0, "", fmt.Errorf("discover: parse disk size %q: %w", sizeStr, err)
	}
	sizeBytes := sectors * 512

	if strings.HasPrefix(deviceName, "nvme") {
		return sizeBytes, "nvme", nil
	}
	switch strings.TrimSpace(rotationalStr) {
	case "1":
		return sizeBytes, "hdd", nil
	case "0":
		return sizeBytes, "ssd", nil
	default:
		return sizeBytes, "", fmt.Errorf("discover: unrecognized rotational value %q", rotationalStr)
	}
}
