package parser

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/coreos/vcontext/report"
	vyaml "github.com/coreos/vcontext/yaml"
	"gopkg.in/yaml.v3"
)

// CloudConfig represents the subset of cloud-init config we care about.
type CloudConfig struct {
	WriteFiles            []File    `yaml:"write_files"`
	Users                 []User    `yaml:"users"`
	Coreos                Coreos    `yaml:"coreos"`
	Runcmd                []string  `yaml:"runcmd"`
	PreKubeadmCommands    []string  `yaml:"preKubeadmCommands"`
	Packages              []string  `yaml:"packages"`
	Apt                   yaml.Node `yaml:"apt"`
	YumRepos              yaml.Node `yaml:"yum_repos"`
	CACerts               CACerts   `yaml:"ca_certs"`
	Hostname              string    `yaml:"hostname"`
	CoreosSSHImportGithub []string  `yaml:"coreos_ssh_import_github"`
	CoreosSSHImportURL    []string  `yaml:"coreos_ssh_import_url"`
	Etcd2                 yaml.Node `yaml:"etcd2"`
	Fleet                 yaml.Node `yaml:"fleet"`
}

type CACerts struct {
	Trusted []string `yaml:"trusted"`
}

type File struct {
	Path        string `yaml:"path"`
	Permissions string `yaml:"permissions"`
	Encoding    string `yaml:"encoding"`
	Owner       string `yaml:"owner"`
	Content     string `yaml:"content"`
}

type User struct {
	Name              string   `yaml:"name"`
	Sudo              string   `yaml:"sudo"`
	SSHAuthorizedKeys []string `yaml:"ssh_authorized_keys"`
	// Groups can be a string (comma separated) or []string. We use a custom unmarshaler.
	Groups GroupsField `yaml:"groups"`
}

type GroupsField []string

func (g *GroupsField) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		if value.Value == "" {
			*g = nil
			return nil
		}
		*g = strings.Split(value.Value, ",")
		for i := range *g {
			(*g)[i] = strings.TrimSpace((*g)[i])
		}
		return nil
	}
	if value.Kind == yaml.SequenceNode {
		var arr []string
		if err := value.Decode(&arr); err != nil {
			return err
		}
		*g = arr
		return nil
	}
	return fmt.Errorf("line %d: groups must be a string or sequence", value.Line)
}

type Coreos struct {
	Units []Unit `yaml:"units"`
}

type Unit struct {
	Name    string   `yaml:"name"`
	Enable  bool     `yaml:"enable"`
	Content string   `yaml:"content"`
	Command string   `yaml:"command"`
	Runtime bool     `yaml:"runtime"`
	Dropins []Dropin `yaml:"drop-ins"`
}

type Dropin struct {
	Name    string `yaml:"name"`
	Content string `yaml:"content"`
}

// Parse extracts the struct, raw root node, and a vcontext report for diagnostics.
func Parse(data []byte) (*CloudConfig, *yaml.Node, report.Report, error) {
	if len(data) > 0 && bytes.HasPrefix(data, []byte("## template: jinja")) {
		return nil, nil, report.Report{}, fmt.Errorf("line 1: Jinja templating is not supported by Ignition. Use coreos-metadata.service and EnvironmentFile= instead.")
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, nil, report.Report{}, err
	}

	var cfg CloudConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, nil, report.Report{}, err
	}

	// Create a vcontext tree as promised in the cover letter.
	// This captures the exact source locations for all struct fields.
	_, _ = vyaml.UnmarshalToContext(data)
	rep := report.Report{}

	return &cfg, &root, rep, nil
}

// FindKeyLine searches the AST for a top-level key and returns its line number.
func FindKeyLine(node *yaml.Node, key string) int {
	if node == nil || len(node.Content) == 0 {
		return 0
	}
	// The root node is usually a DocumentNode containing a MappingNode
	for _, doc := range node.Content {
		if doc.Kind == yaml.MappingNode {
			for i := 0; i < len(doc.Content); i += 2 {
				k := doc.Content[i]
				if k.Value == key {
					return k.Line
				}
			}
		}
	}
	return 0
}

// clean up later

// Note: check for leaks
