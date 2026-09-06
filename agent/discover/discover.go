package discover

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

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
