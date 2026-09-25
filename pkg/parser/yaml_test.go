package parser

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParse_ValidCloudConfig(t *testing.T) {
	input := []byte(`
hostname: worker-node-01
write_files:
  - path: /etc/sysctl.d/99-k8s.conf
    permissions: "0644"
    owner: root:root
    content: |
      net.bridge.bridge-nf-call-iptables = 1
users:
  - name: core
    sudo: "ALL=(ALL) NOPASSWD:ALL"
    groups: sudo, docker
    ssh_authorized_keys:
      - ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI...
runcmd:
  - systemctl restart systemd-sysctl
packages:
  - containerd
ca_certs:
  trusted:
    - |
      -----BEGIN CERTIFICATE-----
      MIIB...
      -----END CERTIFICATE-----
coreos:
  units:
    - name: custom.service
      enable: true
      content: "[Unit]\nDescription=Custom\n"
      drop-ins:
        - name: 10-override.conf
          content: "[Service]\nRestart=always\n"
`)

	cfg, root, _, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse returned unexpected error: %v", err)
	}
	if root == nil {
		t.Fatal("expected non-nil AST root node")
	}

	if cfg.Hostname != "worker-node-01" {
		t.Errorf("expected hostname 'worker-node-01', got %q", cfg.Hostname)
	}

	if len(cfg.WriteFiles) != 1 {
		t.Fatalf("expected 1 write_files entry, got %d", len(cfg.WriteFiles))
	}
	if cfg.WriteFiles[0].Path != "/etc/sysctl.d/99-k8s.conf" {
		t.Errorf("unexpected file path: %s", cfg.WriteFiles[0].Path)
	}
	if cfg.WriteFiles[0].Permissions != "0644" {
		t.Errorf("unexpected file permissions: %s", cfg.WriteFiles[0].Permissions)
	}

	if len(cfg.Users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(cfg.Users))
	}
	if cfg.Users[0].Name != "core" {
		t.Errorf("expected user name 'core', got %q", cfg.Users[0].Name)
	}
	if len(cfg.Users[0].Groups) != 2 || cfg.Users[0].Groups[0] != "sudo" || cfg.Users[0].Groups[1] != "docker" {
		t.Errorf("unexpected user groups: %v", cfg.Users[0].Groups)
	}

	if len(cfg.Runcmd) != 1 || cfg.Runcmd[0] != "systemctl restart systemd-sysctl" {
		t.Errorf("unexpected runcmd: %v", cfg.Runcmd)
	}

	if len(cfg.CACerts.Trusted) != 1 {
		t.Errorf("expected 1 trusted CA cert, got %d", len(cfg.CACerts.Trusted))
	}

	if len(cfg.Coreos.Units) != 1 {
		t.Fatalf("expected 1 coreos unit, got %d", len(cfg.Coreos.Units))
	}
	u := cfg.Coreos.Units[0]
	if u.Name != "custom.service" || !u.Enable {
		t.Errorf("unexpected unit properties: %+v", u)
	}
	if len(u.Dropins) != 1 || u.Dropins[0].Name != "10-override.conf" {
		t.Errorf("unexpected unit dropins: %+v", u.Dropins)
	}
}

func TestParse_JinjaRejection(t *testing.T) {
	input := []byte("## template: jinja\nwrite_files:\n  - path: /etc/test\n")
	cfg, root, _, err := Parse(input)
	if err == nil {
		t.Fatal("expected error for Jinja template, got nil")
	}
	if !strings.Contains(err.Error(), "Jinja templating is not supported by Ignition") {
		t.Errorf("expected Jinja rejection message, got: %v", err)
	}
	if cfg != nil || root != nil {
		t.Errorf("expected nil cfg and root on error, got cfg=%v, root=%v", cfg, root)
	}
}

func TestParse_InvalidYAML(t *testing.T) {
	input := []byte("write_files: [unclosed sequence")
	_, _, _, err := Parse(input)
	if err == nil {
		t.Fatal("expected error on malformed YAML, got nil")
	}
}

func TestGroupsField_UnmarshalYAML(t *testing.T) {
	tests := []struct {
		name      string
		yamlData  string
		expected  []string
		expectErr bool
	}{
		{
			name:     "comma-separated scalar",
			yamlData: "groups: sudo, docker, wheel",
			expected: []string{"sudo", "docker", "wheel"},
		},
		{
			name:     "comma-separated scalar with whitespace",
			yamlData: "groups: \"  admin ,  staff  \"",
			expected: []string{"admin", "staff"},
		},
		{
			name:     "empty scalar string",
			yamlData: "groups: \"\"",
			expected: nil,
		},
		{
			name:     "sequence of strings",
			yamlData: "groups:\n  - sudo\n  - docker",
			expected: []string{"sudo", "docker"},
		},
		{
			name:      "invalid mapping node",
			yamlData:  "groups:\n  invalid: map",
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var wrapper struct {
				Groups GroupsField `yaml:"groups"`
			}
			err := yaml.Unmarshal([]byte(tt.yamlData), &wrapper)
			if tt.expectErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(wrapper.Groups) != len(tt.expected) {
				t.Fatalf("expected %d groups, got %d (%v)", len(tt.expected), len(wrapper.Groups), wrapper.Groups)
			}
			for i := range tt.expected {
				if wrapper.Groups[i] != tt.expected[i] {
					t.Errorf("at index %d: expected %q, got %q", i, tt.expected[i], wrapper.Groups[i])
				}
			}
		})
	}
}

func TestFindKeyLine(t *testing.T) {
	input := []byte(`hostname: my-box
write_files:
  - path: /etc/test
users:
  - name: testuser
`)
	var root yaml.Node
	if err := yaml.Unmarshal(input, &root); err != nil {
		t.Fatalf("failed to unmarshal yaml node: %v", err)
	}

	tests := []struct {
		key          string
		expectedLine int
	}{
		{key: "hostname", expectedLine: 1},
		{key: "write_files", expectedLine: 2},
		{key: "users", expectedLine: 4},
		{key: "non_existent", expectedLine: 0},
	}

	for _, tt := range tests {
		line := FindKeyLine(&root, tt.key)
		if line != tt.expectedLine {
			t.Errorf("FindKeyLine for key %q: expected line %d, got %d", tt.key, tt.expectedLine, line)
		}
	}

	// Test nil root and empty node
	if line := FindKeyLine(nil, "hostname"); line != 0 {
		t.Errorf("expected 0 for nil node, got %d", line)
	}
	if line := FindKeyLine(&yaml.Node{}, "hostname"); line != 0 {
		t.Errorf("expected 0 for empty node, got %d", line)
	}
}

func TestParse_CustomFilePermissions(t *testing.T) {
	input := []byte(`
write_files:
  - path: /etc/security.conf
    permissions: "0600"
    content: "secure"
  - path: /usr/local/bin/script.sh
    permissions: "0755"
    content: "#!/bin/sh"
`)
	cfg, _, _, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if len(cfg.WriteFiles) != 2 {
		t.Fatalf("expected 2 files, got %d", len(cfg.WriteFiles))
	}
	if cfg.WriteFiles[0].Permissions != "0600" {
		t.Errorf("expected 0600, got %s", cfg.WriteFiles[0].Permissions)
	}
	if cfg.WriteFiles[1].Permissions != "0755" {
		t.Errorf("expected 0755, got %s", cfg.WriteFiles[1].Permissions)
	}
}

func TestParse_SingleUserWithoutGroups(t *testing.T) {
	input := []byte(`
users:
  - name: deployer
    sudo: "ALL=(ALL) NOPASSWD:ALL"
`)
	cfg, _, _, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Users) != 1 || cfg.Users[0].Name != "deployer" {
		t.Fatalf("expected 1 user named deployer, got %+v", cfg.Users)
	}
	if len(cfg.Users[0].Groups) != 0 {
		t.Errorf("expected 0 groups, got %v", cfg.Users[0].Groups)
	}
}




func TestParse_MultipleUsersAuthorizedKeys(t *testing.T) {
	input := []byte(`users:
  - name: u1
    ssh_authorized_keys:
      - key1
  - name: u2
    ssh_authorized_keys:
      - key2
`)
	cfg, _, _, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(cfg.Users))
	}
}


func TestParse_EmptyWriteFiles(t *testing.T) {
	input := []byte("write_files: []\n")
	cfg, _, _, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.WriteFiles) != 0 {
		t.Errorf("expected 0 write_files, got %d", len(cfg.WriteFiles))
	}
}


func TestParse_UnitWithEmptyDropins(t *testing.T) {
	input := []byte(`coreos:
  units:
    - name: unit1.service
      enable: true
`)
	cfg, _, _, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Coreos.Units) != 1 {
		t.Fatalf("expected 1 unit, got %d", len(cfg.Coreos.Units))
	}
}


func TestParse_CustomGroupName(t *testing.T) {
	input := []byte(`users:
  - name: admin
    groups: [wheel, systemd-journal]
`)
	cfg, _, _, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Users[0].Groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(cfg.Users[0].Groups))
	}
}


func TestParse_MultipleCACerts(t *testing.T) {
	input := []byte(`ca_certs:
  trusted:
    - cert1
    - cert2
`)
	cfg, _, _, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.CACerts.Trusted) != 2 {
		t.Fatalf("expected 2 CA certs, got %d", len(cfg.CACerts.Trusted))
	}
}


func TestParse_SingleGroupScalar(t *testing.T) {
	input := []byte(`users:
  - name: testuser
    groups: sudo
`)
	cfg, _, _, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Users[0].Groups) != 1 || cfg.Users[0].Groups[0] != "sudo" {
		t.Errorf("expected 1 group [sudo], got %v", cfg.Users[0].Groups)
	}
}


func TestParse_MultiLineScriptContent(t *testing.T) {
	input := []byte(`write_files:
  - path: /opt/bin/run.sh
    content: |
      #!/bin/sh
      echo "hello world"
`)
	cfg, _, _, err := Parse(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.WriteFiles) != 1 {
		t.Fatalf("expected 1 file, got %d", len(cfg.WriteFiles))
	}
}

