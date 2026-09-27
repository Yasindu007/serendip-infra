package bootstrap

import (
	"slices"
	"testing"
)

func TestInstallEnv_DefaultVersion(t *testing.T) {
	got := InstallEnv(InstallOptions{})
	want := "INSTALL_K3S_VERSION=" + DefaultVersion
	if !slices.Contains(got, want) {
		t.Errorf("InstallEnv({}) = %v, want it to contain %q", got, want)
	}
}

func TestInstallEnv_OverriddenVersion(t *testing.T) {
	got := InstallEnv(InstallOptions{Version: "v1.30.0+k3s1"})
	want := "INSTALL_K3S_VERSION=v1.30.0+k3s1"
	if !slices.Contains(got, want) {
		t.Errorf("InstallEnv({Version: v1.30.0+k3s1}) = %v, want it to contain %q", got, want)
	}
	if slices.Contains(got, "INSTALL_K3S_VERSION="+DefaultVersion) {
		t.Errorf("InstallEnv() with an explicit version = %v, should not also carry the default version", got)
	}
}
