// Package config loads the description of how a fork follows its upstream.
package config

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/spf13/viper"
)

// DefaultPath is where the configuration lives, relative to the repository root.
const DefaultPath = ".github/fork-sync.yaml"

// Config describes how a fork follows the releases of its upstream.
type Config struct {
	// Upstream is the URL of the upstream repository.
	Upstream string `mapstructure:"upstream"`
	// BaseBranch is the fork's branch that sync pull requests target.
	BaseBranch string `mapstructure:"baseBranch"`
	// OwnedPaths are git pathspecs (files or directories) that the fork owns:
	// what upstream changes there is not taken.
	OwnedPaths []string `mapstructure:"ownedPaths"`
	// Rename renames upstream's names in the fork's files. It runs after the
	// merge, before the adaptations, and what it changes is committed with them.
	// Nil when the fork renames nothing.
	Rename *Rename `mapstructure:"rename"`
	// Adaptations run in order after the rename. What they change is committed.
	Adaptations []Adaptation `mapstructure:"adaptations"`
	// PRBodyTemplate is a text/template file, relative to the repository root,
	// for the body of the sync pull request. Required.
	PRBodyTemplate string `mapstructure:"prBodyTemplate"`
}

// Rename describes the text replacements that turn upstream's names into the
// fork's. The owned paths are never renamed.
type Rename struct {
	// Files are git pathspecs ("*.go", "docs/*.md") of the files to rename in.
	Files []string `mapstructure:"files"`
	// Replace are applied in order to the content of each file.
	Replace []Replacement `mapstructure:"replace"`
	// ShortNames renames the entries of the shortNames lists in CRDs, which are
	// too short to be replaced everywhere.
	ShortNames []Replacement `mapstructure:"shortNames"`
	// CRDDir is the directory of the generated CRDs. Upstream's CRD files there
	// are kept as they are, and the renamed CRDs are written next to them under
	// the renamed file name.
	CRDDir string `mapstructure:"crdDir"`
}

// Replacement replaces the text From with To.
type Replacement struct {
	From string `mapstructure:"from"`
	To   string `mapstructure:"to"`
}

// Adaptation is a program run in the repository after the merge.
type Adaptation struct {
	// Name identifies the command in commit messages and logs.
	Name string `mapstructure:"name"`
	// Run is the program and its arguments.
	Run []string `mapstructure:"run"`
	// AllowFailure makes a failing adaptation drop its changes and get
	// reported, instead of stopping the sync.
	AllowFailure bool `mapstructure:"allowFailure"`
}

// Load reads the configuration at path, relative to the repository root dir,
// fills in defaults and validates it. Unknown keys are an error, so that a typo
// does not silently disable something.
func Load(dir, path string) (Config, error) {
	v := viper.New()
	v.SetConfigFile(filepath.Join(dir, path))
	v.SetConfigType("yaml")
	v.SetDefault("baseBranch", "main")
	err := v.ReadInConfig()
	if err != nil {
		return Config{}, fmt.Errorf("failed to read %s: %w", path, err)
	}

	var cfg Config
	err = v.UnmarshalExact(&cfg)
	if err != nil {
		return Config{}, fmt.Errorf("failed to parse %s: %w", path, err)
	}
	err = cfg.validate()
	if err != nil {
		return Config{}, fmt.Errorf("invalid %s: %w", path, err)
	}
	return cfg, nil
}

func (c Config) validate() error {
	if c.Upstream == "" {
		return errors.New("upstream is required")
	}
	if c.Rename != nil {
		if len(c.Rename.Files) == 0 {
			return errors.New("rename has no files")
		}
		for _, list := range [][]Replacement{c.Rename.Replace, c.Rename.ShortNames} {
			for _, r := range list {
				if r.From == "" {
					return fmt.Errorf("rename: the replacement with %q has nothing to replace", r.To)
				}
			}
		}
	}
	for i, cmd := range c.Adaptations {
		if cmd.Name == "" {
			return fmt.Errorf("adaptation %d has no name", i)
		}
		if len(cmd.Run) == 0 {
			return fmt.Errorf("adaptation %q has nothing to run", cmd.Name)
		}
	}
	if c.PRBodyTemplate == "" {
		return errors.New("prBodyTemplate is required")
	}
	return nil
}
