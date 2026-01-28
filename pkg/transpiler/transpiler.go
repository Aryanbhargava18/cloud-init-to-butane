package transpiler

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/Aryanbhargava18/cloud-init-to-butane/pkg/parser"
	"gopkg.in/yaml.v3"
)

// Butane Config represents Butane v1.0.0
type Config struct {
	Variant string  `yaml:"variant"`
	Version string  `yaml:"version"`
	Storage Storage `yaml:"storage,omitempty"`
	Passwd  Passwd  `yaml:"passwd,omitempty"`
	Systemd Systemd `yaml:"systemd,omitempty"`
}

type Storage struct {
	Files []File `yaml:"files,omitempty"`
}

type File struct {
	Path     string      `yaml:"path"`
	Mode     *int        `yaml:"mode,omitempty"`
	Contents FileContent `yaml:"contents"`
	User     *NodeUser   `yaml:"user,omitempty"`
	Group    *NodeGroup  `yaml:"group,omitempty"`
}

type FileContent struct {
	Inline      *string `yaml:"inline,omitempty"`
	Compression *string `yaml:"compression,omitempty"`
}

type NodeUser struct {
	Name string `yaml:"name"`
}

type NodeGroup struct {
	Name string `yaml:"name"`
}

type Passwd struct {
	Users []User `yaml:"users,omitempty"`
}

type User struct {
	Name              string   `yaml:"name"`
	SSHAuthorizedKeys []string `yaml:"ssh_authorized_keys,omitempty"`
	Groups            []string `yaml:"groups,omitempty"`
}

type Systemd struct {
	Units []Unit `yaml:"units,omitempty"`
}

type Unit struct {
	Name     string   `yaml:"name"`
	Enabled  *bool    `yaml:"enabled,omitempty"`
	Contents *string  `yaml:"contents,omitempty"`
	Dropins  []Dropin `yaml:"dropins,omitempty"`
}

type Dropin struct {
	Name     string  `yaml:"name"`
	Contents *string `yaml:"contents,omitempty"`
}

func Transpile(cfg *parser.CloudConfig, root *yaml.Node) (*Config, error) {
	out := &Config{
		Variant: "flatcar",
		Version: "1.0.0",
	}

	// 1. Refused Bucket
	if len(cfg.Packages) > 0 {
		line := parser.FindKeyLine(root, "packages")
		return nil, fmt.Errorf("line %d: packages has no Flatcar equivalent; see systemd-sysext", line)
	}
	if !cfg.Apt.IsZero() {
		return nil, fmt.Errorf("line %d: apt package manager not supported; see systemd-sysext", cfg.Apt.Line)
	}

	// Refused: Deprecated keys
	if len(cfg.CoreosSSHImportGithub) > 0 || len(cfg.CoreosSSHImportURL) > 0 || !cfg.Etcd2.IsZero() || !cfg.Fleet.IsZero() {
		return nil, fmt.Errorf("coreos_ssh_import_github, coreos_ssh_import_url, etcd2, and fleet are deprecated and not supported in Ignition 3.x.")
	}
	if !cfg.YumRepos.IsZero() {
		return nil, fmt.Errorf("line %d: yum_repos not supported; see systemd-sysext", cfg.YumRepos.Line)
	}
	for _, u := range cfg.Coreos.Units {
		if u.Command != "" {
			return nil, fmt.Errorf("coreos.units[name=%s].command has no declarative equivalent in Butane", u.Name)
		}
		if u.Runtime {
			return nil, fmt.Errorf("coreos.units[name=%s].runtime has no declarative equivalent in Butane", u.Name)
		}
	}

	// 2. Direct Bucket


	for _, f := range cfg.WriteFiles {
		tf := transpileFile(f)

		// 3. Lowered Bucket: /run or /tmp staging
		if strings.HasPrefix(f.Path, "/run/") || strings.HasPrefix(f.Path, "/tmp/") {
			stagedPath := "/var/tmp/cloud-init-staged" + f.Path

			// Add the actual file to storage at the staged path
			tf.Path = stagedPath
			out.Storage.Files = append(out.Storage.Files, tf)

			// Add a tmpfiles.d config to copy it on boot
			tmpName := strings.ReplaceAll(strings.TrimPrefix(f.Path, "/"), "/", "-")
			tmpfilesPath := fmt.Sprintf("/etc/tmpfiles.d/cloud-init-%s.conf", tmpName)

			modeStr := "0644"
			if tf.Mode != nil {
				modeStr = fmt.Sprintf("%04o", *tf.Mode)
			}

			user := "-"
			group := "-"
			if f.Owner != "" {
				parts := strings.Split(f.Owner, ":")
				user = parts[0]
				if len(parts) > 1 {
					group = parts[1]
				} else {
					group = parts[0]
				}
			}

			content := fmt.Sprintf("C %s %s %s %s - %s\n", f.Path, modeStr, user, group, stagedPath)
			mode := 0644

			out.Storage.Files = append(out.Storage.Files, File{
				Path: tmpfilesPath,
				Mode: &mode,
				Contents: FileContent{
					Inline: &content,
				},
			})
			continue
		}

		// Handle networkd files per coreos-cloudinit logic
		if isNetworkd(f.Path) {
			// They go into storage.files, not systemd.units
			tf.Path = "/etc/systemd/network/" + f.Path
		}

		out.Storage.Files = append(out.Storage.Files, tf)
	}

	for _, u := range cfg.Users {
		tu := User{
			Name:              u.Name,
			SSHAuthorizedKeys: u.SSHAuthorizedKeys,
			Groups:            u.Groups,
		}
		out.Passwd.Users = append(out.Passwd.Users, tu)

		// Lowered: sudoers
		if u.Sudo != "" {
			mode := 0440
			content := u.Sudo
			out.Storage.Files = append(out.Storage.Files, File{
				Path:     fmt.Sprintf("/etc/sudoers.d/%s", u.Name),
				Mode:     &mode,
				Contents: FileContent{Inline: &content},
			})
		}
	}

	// Lowered: ca_certs
	if len(cfg.CACerts.Trusted) > 0 {
		for i, cert := range cfg.CACerts.Trusted {
			mode := 0644
			content := cert
			out.Storage.Files = append(out.Storage.Files, File{
				Path:     fmt.Sprintf("/etc/ssl/certs/cloud-init-%d.pem", i),
				Mode:     &mode,
				Contents: FileContent{Inline: &content},
			})
		}

		// Add unit to update trust store
		enabled := true
		content := `[Unit]
Description=Update TLS Certificates
ConditionPathExistsGlob=/etc/ssl/certs/cloud-init-*.pem

[Service]
Type=oneshot
ExecStart=/usr/sbin/update-ca-certificates

[Install]
WantedBy=multi-user.target
`
		out.Systemd.Units = append(out.Systemd.Units, Unit{
			Name:     "update-ca-certificates.service",
			Enabled:  &enabled,
			Contents: &content,
		})
	}

	// Lowered: hostname
	if cfg.Hostname != "" {
		mode := 0644
		content := cfg.Hostname
		out.Storage.Files = append(out.Storage.Files, File{
			Path:     "/etc/hostname",
			Mode:     &mode,
			Contents: FileContent{Inline: &content},
		})
	}

	for _, u := range cfg.Coreos.Units {
		tu := Unit{
			Name: u.Name,
		}
		if u.Enable {
			v := true
			tu.Enabled = &v
		}
		if u.Content != "" {
			content := u.Content
			tu.Contents = &content
		}
		for _, d := range u.Dropins {
			content := d.Content
			tu.Dropins = append(tu.Dropins, Dropin{
				Name:     d.Name,
				Contents: &content,
			})
		}
		out.Systemd.Units = append(out.Systemd.Units, tu)
	}

	// Lowered: runcmd / preKubeadmCommands
	allCmds := append(cfg.PreKubeadmCommands, cfg.Runcmd...)
	if len(allCmds) > 0 {
		script := "#!/bin/sh\n# Generated from cloud-config runcmd/preKubeadmCommands\n" + strings.Join(allCmds, "\n")

		// Create the script file
		mode := 0755
		out.Storage.Files = append(out.Storage.Files, File{
			Path:     "/opt/cloud-init/runcmd.sh",
			Mode:     &mode,
			Contents: FileContent{Inline: &script},
		})

		// Create the unit that runs it
		content := `[Unit]
Description=Cloud-init Runcmd (Generated from cloud-config runcmd)
Requires=containerd.service
After=systemd-tmpfiles-setup.service containerd.service
ConditionPathExists=!/var/lib/cloud-init-runcmd-done
ConditionPathExists=!/etc/kubernetes/kubelet.conf

[Service]
Type=oneshot
ExecStart=/opt/cloud-init/runcmd.sh
ExecStartPost=/bin/touch /var/lib/cloud-init-runcmd-done
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
`
		enabled := true
		out.Systemd.Units = append(out.Systemd.Units, Unit{
			Name:     "cloud-init-runcmd.service",
			Enabled:  &enabled,
			Contents: &content,
		})
	}

	return out, nil
}

func transpileFile(f parser.File) File {
	tf := File{
		Path: f.Path,
	}

	if f.Permissions != "" {
		// cloud-init permissions can be quoted "0640".
		// We parse it as an octal integer; non-parseable values default to standard 0644 in Ignition.
		val := strings.Trim(f.Permissions, `"'`)
		if parsed, err := strconv.ParseInt(val, 8, 32); err == nil {
			mode := int(parsed)
			tf.Mode = &mode
		}
	}

	if f.Owner != "" {
		parts := strings.SplitN(f.Owner, ":", 2)
		tf.User = &NodeUser{Name: parts[0]}
		if len(parts) > 1 {
			tf.Group = &NodeGroup{Name: parts[1]}
		}
	}

	content := f.Content
	// Direct bucket: decode b64/gz at transpile time
	if f.Encoding == "base64" || f.Encoding == "b64" {
		if dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(content)); err == nil {
			content = string(dec)
		}
	} else if f.Encoding == "gz" {
		if dec, err := readGz([]byte(content)); err == nil {
			content = string(dec)
		}
	} else if f.Encoding == "gz+base64" || f.Encoding == "gz+b64" {
		if dec, err := base64.StdEncoding.DecodeString(strings.TrimSpace(content)); err == nil {
			if unzipped, err := readGz(dec); err == nil {
				content = string(unzipped)
			}
		}
	}

	tf.Contents.Inline = &content
	return tf
}

func readGz(data []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

func isNetworkd(path string) bool {
	return strings.HasSuffix(path, ".network") || strings.HasSuffix(path, ".netdev") || strings.HasSuffix(path, ".link")
}

// Note: check for leaks

// clean up later

// Octal file permissions are formatted to 4-character strings for Ignition specification
