// Package rename turns upstream's names (API groups, finalizers, short names)
// into the fork's, in the files of the fork.
//
// The replacements are plain text, applied in the order they are configured.
// Applying them twice changes nothing, as long as no replacement's result
// contains what an earlier or later replacement replaces.
package rename

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/qdrant/fluxcd-helm-controller/tools/fork-sync/internal/config"
	"github.com/qdrant/fluxcd-helm-controller/tools/fork-sync/internal/git"
)

// Apply renames in the files of repo that cfg.Files selects, tracked or not
// (ignored files excepted), leaving out the paths in exclude. It returns the
// paths it wrote, relative to the repository root.
//
// In cfg.CRDDir, a file whose name has something to rename is not changed: the
// renamed CRD is written next to it, under the renamed file name.
func Apply(repo git.Repo, cfg config.Rename, exclude []string) ([]string, error) {
	files, err := Files(repo, cfg, exclude)
	if err != nil {
		return nil, err
	}

	var written []string
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(repo.Dir, file))
		if errors.Is(err, fs.ErrNotExist) {
			continue // deleted in the working tree
		}
		if err != nil {
			return nil, err
		}
		renamed := Content(cfg, string(data))

		target := Target(cfg, file)
		changed, err := writeIfChanged(filepath.Join(repo.Dir, target), renamed)
		if err != nil {
			return nil, err
		}
		if changed {
			written = append(written, target)
		}
	}
	return written, nil
}

// Target returns the path Apply writes the renamed content of file to: file
// itself, except for a CRD in cfg.CRDDir whose name has something to rename,
// which keeps upstream's content and gets a renamed copy under the renamed name.
func Target(cfg config.Rename, file string) string {
	if cfg.CRDDir == "" || path.Dir(file) != path.Clean(cfg.CRDDir) {
		return file
	}
	return path.Join(path.Dir(file), replace(cfg.Replace, path.Base(file)))
}

// Content returns content with the replacements and the short name renames of
// cfg applied.
func Content(cfg config.Rename, content string) string {
	return renameShortNames(cfg.ShortNames, replace(cfg.Replace, content))
}

func replace(replacements []config.Replacement, s string) string {
	for _, r := range replacements {
		s = strings.ReplaceAll(s, r.From, r.To)
	}
	return s
}

// renameShortNames renames the entries of the shortNames lists, as CRDs have
// them:
//
//	shortNames:
//	- gitrepo
//
// Only whole entries are renamed: "- gitrepository" stays as it is.
func renameShortNames(renames []config.Replacement, content string) string {
	if len(renames) == 0 {
		return content
	}
	lines := strings.Split(content, "\n")
	inList := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "shortNames:":
			inList = true
		case inList && strings.HasPrefix(trimmed, "- "):
			for _, r := range renames {
				if trimmed == "- "+r.From {
					lines[i] = strings.Replace(line, "- "+r.From, "- "+r.To, 1)
					break
				}
			}
		default:
			inList = false
		}
	}
	return strings.Join(lines, "\n")
}

// Files returns the files of repo that the rename applies to: those cfg.Files
// selects, tracked or not (ignored files excepted), except the paths in
// exclude.
func Files(repo git.Repo, cfg config.Rename, exclude []string) ([]string, error) {
	lsFiles := []string{"ls-files", "-z", "--cached", "--others", "--exclude-standard", "--deduplicate", "--"}
	return repo.Paths(slices.Concat(lsFiles, cfg.Files, git.Exclude(exclude))...)
}

// writeIfChanged writes content to the file unless it already has it, and
// reports whether it wrote it. A new file gets the usual permissions.
func writeIfChanged(file, content string) (bool, error) {
	current, err := os.ReadFile(file)
	if err == nil && string(current) == content {
		return false, nil
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	return true, os.WriteFile(file, []byte(content), 0o644)
}
