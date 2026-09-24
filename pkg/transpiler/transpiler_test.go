package transpiler

import (
	"strings"
	"testing"

	"github.com/Aryanbhargava18/cloud-init-to-butane/pkg/parser"
)

func TestTranspileDirect(t *testing.T) {
	input := []byte(`
write_files:
  - path: /etc/test.conf
    permissions: "0640"
    owner: root:root
    content: |
      test
users:
  - name: aryan
    groups: docker, wheel
    ssh_authorized_keys:
      - ssh-rsa AAAAB3...
`)

	cfg, root, _, err := parser.Parse(input)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	out, err := Transpile(cfg, root)
	if err != nil {
		t.Fatalf("Transpile failed: %v", err)
	}

	if len(out.Storage.Files) != 1 {
		t.Errorf("Expected 1 file, got %d", len(out.Storage.Files))
	} else {
		f := out.Storage.Files[0]
		if *f.Mode != 0640 {
			t.Errorf("Expected mode 0640, got %o", *f.Mode)
		}
		if f.User.Name != "root" || f.Group.Name != "root" {
			t.Errorf("Expected owner root:root")
		}
	}

	if len(out.Passwd.Users) != 1 {
		t.Errorf("Expected 1 user, got %d", len(out.Passwd.Users))
	} else {
		u := out.Passwd.Users[0]
		if len(u.Groups) != 2 || u.Groups[0] != "docker" {
			t.Errorf("Expected parsed groups array, got %v", u.Groups)
		}
	}
}

func TestTranspileLoweredRuncmd(t *testing.T) {
	input := []byte(`
runcmd:
  - kubeadm join --config /run/kubeadm/kubeadm-join-config.yaml
`)

	cfg, root, _, err := parser.Parse(input)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	out, err := Transpile(cfg, root)
	if err != nil {
		t.Fatalf("Transpile failed: %v", err)
	}

	// Should create the script file and the service unit
	if len(out.Storage.Files) != 1 {
		t.Errorf("Expected 1 script file, got %d", len(out.Storage.Files))
	}
	if len(out.Systemd.Units) != 1 {
		t.Errorf("Expected 1 service unit, got %d", len(out.Systemd.Units))
	} else {
		unit := out.Systemd.Units[0]
		if unit.Name != "cloud-init-runcmd.service" {
			t.Errorf("Expected runcmd service")
		}
		if !strings.Contains(*unit.Contents, "After=systemd-tmpfiles-setup.service") {
			t.Errorf("Expected correct unit ordering")
		}
	}
}

func TestTranspileRefusedJinja(t *testing.T) {
	input := []byte(`## template: jinja
#cloud-config
write_files: []
`)
	_, _, _, err := parser.Parse(input)
	if err == nil {
		t.Fatalf("Expected error for Jinja template")
	}
	if !strings.Contains(err.Error(), "line 1") || !strings.Contains(err.Error(), "coreos-metadata") {
		t.Errorf("Expected helpful error message pointing to coreos-metadata, got: %v", err)
	}
}

func TestTranspileRefusedPackages(t *testing.T) {
	input := []byte(`
packages:
  - htop
`)
	cfg, root, _, err := parser.Parse(input)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	_, err = Transpile(cfg, root)
	if err == nil {
		t.Fatalf("Expected error for packages")
	}
	if !strings.Contains(err.Error(), "line 2") || !strings.Contains(err.Error(), "systemd-sysext") {
		t.Errorf("Expected helpful error message pointing to systemd-sysext with line number, got: %v", err)
	}
}

func TestTranspileLoweredRunPath(t *testing.T) {
	input := []byte(`
write_files:
  - path: /run/kubeadm/kubeadm-join-config.yaml
    content: |
      test
`)
	cfg, root, _, err := parser.Parse(input)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	out, err := Transpile(cfg, root)
	if err != nil {
		t.Fatalf("Transpile failed: %v", err)
	}

	// Should create staged file and tmpfiles.d config
	if len(out.Storage.Files) != 2 {
		t.Errorf("Expected 2 files (staged + tmpfiles.d), got %d", len(out.Storage.Files))
	} else {
		if !strings.HasPrefix(out.Storage.Files[0].Path, "/var/tmp") {
			t.Errorf("First file should be staged")
		}
		if !strings.HasPrefix(out.Storage.Files[1].Path, "/etc/tmpfiles.d") {
			t.Errorf("Second file should be tmpfiles.d")
		}
	}
}

func TestTranspileLoweredCaCerts(t *testing.T) {
	input := []byte(`
ca_certs:
  trusted:
    - |
      -----BEGIN CERTIFICATE-----
      MII...
      -----END CERTIFICATE-----
`)
	cfg, root, _, err := parser.Parse(input)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	out, err := Transpile(cfg, root)
	if err != nil {
		t.Fatalf("Transpile failed: %v", err)
	}

	if len(out.Storage.Files) != 1 || out.Storage.Files[0].Path != "/etc/ssl/certs/cloud-init-0.pem" {
		t.Errorf("Expected cert file at /etc/ssl/certs/cloud-init-0.pem")
	}
	if len(out.Systemd.Units) != 1 || out.Systemd.Units[0].Name != "update-ca-certificates.service" {
		t.Errorf("Expected update-ca-certificates.service")
	}
}

func TestTranspileRefusedCommandRuntime(t *testing.T) {
	input := []byte(`
coreos:
  units:
    - name: test.service
      command: start
      runtime: true
`)
	cfg, root, _, err := parser.Parse(input)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	_, err = Transpile(cfg, root)
	if err == nil {
		t.Fatalf("Expected error for command/runtime")
	}
	if !strings.Contains(err.Error(), "has no declarative equivalent in Butane") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

func TestTranspileRefusedDeprecated(t *testing.T) {
	input := []byte(`
coreos_ssh_import_github:
  - user
`)
	cfg, root, _, err := parser.Parse(input)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	_, err = Transpile(cfg, root)
	if err == nil {
		t.Fatalf("Expected error for deprecated keys")
	}
	if !strings.Contains(err.Error(), "deprecated and not supported") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

func TestTranspileHostname(t *testing.T) {
	input := []byte(`
hostname: flatcar-node
`)
	cfg, root, _, err := parser.Parse(input)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	out, err := Transpile(cfg, root)
	if err != nil {
		t.Fatalf("Transpile failed: %v", err)
	}

	if len(out.Storage.Files) != 1 || out.Storage.Files[0].Path != "/etc/hostname" {
		t.Errorf("Expected hostname to lower to /etc/hostname")
	}
	content := *out.Storage.Files[0].Contents.Inline
	if content != "flatcar-node" {
		t.Errorf("Expected hostname content to be flatcar-node, got %s", content)
	}
}

func TestTranspileEmptyRuncmd(t *testing.T) {
	input := []byte(`
hostname: test-worker
`)
	cfg, root, _, err := parser.Parse(input)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	out, err := Transpile(cfg, root)
	if err != nil {
		t.Fatalf("Transpile failed: %v", err)
	}
	// Only /etc/hostname should be present, no runcmd script
	if len(out.Storage.Files) != 1 {
		t.Errorf("Expected 1 file (hostname), got %d", len(out.Storage.Files))
	}
	if len(out.Systemd.Units) != 0 {
		t.Errorf("Expected 0 units when runcmd is absent, got %d", len(out.Systemd.Units))
	}
}

