package transpiler_test

import (
	"os"
	"strings"
	"testing"

	"github.com/Aryanbhargava18/cloud-init-to-butane/pkg/parser"
	"github.com/Aryanbhargava18/cloud-init-to-butane/pkg/transpiler"
)

// TestCAPIWorkerE2E transpiles the representative CAPI worker cloud-config
// (testdata/capi-worker.yaml) and asserts the key semantic properties that
// make the output correct for Flatcar / Ignition v3.
//
// The CAPI worker template writes kubeadm config to /run/kubeadm/, which is a
// tmpfs mount point that does not exist when Ignition runs. This test verifies
// the transpiler correctly implements the staging + tmpfiles.d pattern instead
// of naively mapping /run writes to Ignition storage.
func TestCAPIWorkerE2E(t *testing.T) {
	data, err := os.ReadFile("../../testdata/capi-worker.yaml")
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}

	cfg, root, _, err := parser.Parse(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	out, err := transpiler.Transpile(cfg, root)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}

	// --- Variant/Version ---
	if out.Variant != "flatcar" {
		t.Errorf("variant: want flatcar, got %s", out.Variant)
	}
	if out.Version != "1.0.0" {
		t.Errorf("version: want 1.0.0, got %s", out.Version)
	}

	// Index the output files by path for easy lookup.
	files := make(map[string]transpiler.File)
	for _, f := range out.Storage.Files {
		files[f.Path] = f
	}

	// --- /run staging ---
	// /run/kubeadm/kubeadm-join-config.yaml must NOT appear at its original
	// path. Instead it must be staged to /var/tmp/cloud-init-staged/...
	if _, ok := files["/run/kubeadm/kubeadm-join-config.yaml"]; ok {
		t.Error("/run/kubeadm/kubeadm-join-config.yaml must not be written directly (tmpfs not mounted at Ignition time)")
	}
	stagedJoin := "/var/tmp/cloud-init-staged/run/kubeadm/kubeadm-join-config.yaml"
	if _, ok := files[stagedJoin]; !ok {
		t.Errorf("staged join config missing: want %s", stagedJoin)
	}
	if _, ok := files["/run/cluster-api/placeholder"]; ok {
		t.Error("/run/cluster-api/placeholder must not be written directly")
	}
	stagedPlaceholder := "/var/tmp/cloud-init-staged/run/cluster-api/placeholder"
	if _, ok := files[stagedPlaceholder]; !ok {
		t.Errorf("staged placeholder missing: want %s", stagedPlaceholder)
	}

	// --- tmpfiles.d copy rules ---
	// Each /run file must have a corresponding /etc/tmpfiles.d/*.conf that
	// copies it into place once tmpfs mounts are available.
	tmpfilesJoin := "/etc/tmpfiles.d/cloud-init-run-kubeadm-kubeadm-join-config.yaml.conf"
	if f, ok := files[tmpfilesJoin]; !ok {
		t.Errorf("tmpfiles.d rule missing for join config: want %s", tmpfilesJoin)
	} else {
		// Rule must reference the correct source (staged) and target (/run) paths.
		if f.Contents.Inline == nil || !strings.Contains(*f.Contents.Inline, stagedJoin) {
			t.Errorf("tmpfiles.d rule must reference staged path %s", stagedJoin)
		}
		if f.Contents.Inline == nil || !strings.Contains(*f.Contents.Inline, "/run/kubeadm/kubeadm-join-config.yaml") {
			t.Errorf("tmpfiles.d rule must reference target /run path")
		}
	}

	// --- Regular file passes through directly ---
	if _, ok := files["/etc/containerd/config.toml"]; !ok {
		t.Error("/etc/containerd/config.toml must appear directly in storage.files")
	}

	// --- CA certificates ---
	// ca_certs.trusted must be lowered to /etc/ssl/certs/*.pem files.
	certFile := "/etc/ssl/certs/cloud-init-0.pem"
	if _, ok := files[certFile]; !ok {
		t.Errorf("CA cert file missing: want %s", certFile)
	}

	// --- Users ---
	if len(out.Passwd.Users) != 1 || out.Passwd.Users[0].Name != "core" {
		t.Errorf("expected user 'core', got %+v", out.Passwd.Users)
	}

	// --- Systemd units ---
	units := make(map[string]transpiler.Unit)
	for _, u := range out.Systemd.Units {
		units[u.Name] = u
	}

	// runcmd → oneshot service
	runcmdSvc, ok := units["cloud-init-runcmd.service"]
	if !ok {
		t.Fatal("cloud-init-runcmd.service missing")
	}
	if runcmdSvc.Enabled == nil || !*runcmdSvc.Enabled {
		t.Error("cloud-init-runcmd.service must be enabled")
	}
	// CRITICAL: the runcmd service must order After=systemd-tmpfiles-setup.service
	// so that staged /run files are materialized before kubeadm join runs.
	if runcmdSvc.Contents == nil || !strings.Contains(*runcmdSvc.Contents, "After=systemd-tmpfiles-setup.service") {
		t.Error("cloud-init-runcmd.service must have After=systemd-tmpfiles-setup.service to ensure staged files are ready before kubeadm join")
	}

	// CA cert updater unit
	if _, ok := units["update-ca-certificates.service"]; !ok {
		t.Error("update-ca-certificates.service missing")
	}

	// runcmd script must contain the kubeadm join command.
	runcmdScript, ok := files["/opt/cloud-init/runcmd.sh"]
	if !ok {
		t.Fatal("/opt/cloud-init/runcmd.sh missing")
	}
	if runcmdScript.Contents.Inline == nil || !strings.Contains(*runcmdScript.Contents.Inline, "kubeadm join") {
		t.Error("runcmd script must contain kubeadm join command")
	}
}

// Note: check for leaks
