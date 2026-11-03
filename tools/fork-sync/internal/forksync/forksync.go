// Package forksync merges a release of a fork's upstream into the fork, the way
// the sync pull requests are built.
//
// A sync branch gets up to two commits, so that each can be reviewed on its own:
//
//  1. "chore: merge upstream tag vX.Y.Z": upstream's changes since the last sync,
//     merged automatically, with nothing of the fork's added. The paths the
//     fork owns take upstream's version here.
//  2. "chore: apply Qdrant adaptations ...": the fork's version of those paths
//     put back, plus what the configured rename and adaptations change. Skipped
//     when there is nothing to change.
//
// A merge with conflicts is committed as it is, with a file listing the
// conflicting files (some conflicts leave no markers), so that CI fails until
// someone resolves them. The fork-owned paths are still put back, in a commit of
// their own, so that upstream's versions of them never end up on the branch.
package forksync

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/go-logr/logr"
	"golang.org/x/mod/semver"

	"github.com/qdrant/fluxcd-helm-controller/tools/fork-sync/internal/config"
	"github.com/qdrant/fluxcd-helm-controller/tools/fork-sync/internal/git"
	"github.com/qdrant/fluxcd-helm-controller/tools/fork-sync/internal/rename"
)

const (
	// upstreamRefs is where upstream's tags are fetched, apart from the fork's
	// own tags (vX.Y.Z-qdrant.N).
	upstreamRefs = "refs/upstream-tags/"
	// ConflictsFile lists the conflicting files of a merge with conflicts. It is
	// committed, and CI fails while it exists.
	ConflictsFile = ".sync-upstream-conflicts"
	// BranchPrefix starts the name of every sync branch.
	BranchPrefix = "sync/upstream-"
	// workflowsDir holds the GitHub Actions workflows. The sync never changes
	// them: the sync pull request's CI runs them with this repository's
	// secrets, before anyone reviews it.
	workflowsDir = ".github/workflows/"
)

// stableTag matches upstream's stable releases. Pre-releases and the tags of
// the api/ module (api/vX.Y.Z) do not match.
var stableTag = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

// ErrInvalidTag is returned for a tag that is not a stable release (vX.Y.Z).
var ErrInvalidTag = errors.New("not a stable upstream release tag (expected vX.Y.Z)")

// Syncer syncs a fork with its upstream.
type Syncer struct {
	Repo   git.Repo
	Config config.Config
	// Logger reports what the sync does. The zero value logs nothing.
	Logger logr.Logger
}

// Target is the upstream release to sync.
type Target struct {
	Tag string
	// UpToDate is true when the release is already merged into the fork.
	UpToDate bool
}

// Branch is the name of the sync branch for the release.
func (t Target) Branch() string {
	return BranchPrefix + t.Tag
}

// Status is the outcome of a merge.
type Status string

const (
	// StatusMerged means the merge was clean and the adaptations were applied.
	StatusMerged Status = "merged"
	// StatusConflict means the merge has conflicts, committed as they are.
	StatusConflict Status = "conflict"
)

// Result is the outcome of Merge.
type Result struct {
	Tag    string
	Branch string
	// Base is the commit the sync branch starts from.
	Base string
	// Synced is the latest upstream release the fork had before this sync, or
	// "" if it had none.
	Synced string
	Status Status
	// Conflicts are the conflicting files, for StatusConflict.
	Conflicts []string
	// Restored are the fork-owned paths put back after the merge, with their
	// status relative to the merge commit.
	Restored []git.Change
	// FailedAdaptations are the adaptations that failed and were allowed to:
	// their changes are not on the branch.
	FailedAdaptations []string
}

// FindTarget fetches upstream's tags and returns the release to sync: tag, or
// the latest stable release when tag is empty.
func (s *Syncer) FindTarget(tag string) (Target, error) {
	err := s.fetchUpstreamTags()
	if err != nil {
		return Target{}, err
	}
	if tag == "" {
		var err error
		tag, err = s.latestStableTag()
		if err != nil {
			return Target{}, err
		}
	}
	err = s.checkTag(tag)
	if err != nil {
		return Target{}, err
	}
	target := Target{
		Tag:      tag,
		UpToDate: s.Repo.Succeeds("merge-base", "--is-ancestor", upstreamRefs+tag, "HEAD"),
	}
	s.Logger.Info("Found the upstream release", "tag", tag, "upToDate", target.UpToDate)
	return target, nil
}

// Merge creates the sync branch for the upstream release tag from HEAD and
// commits the merge and the adaptations to it. A merge with conflicts is not
// an error: it is committed as it is and reported with StatusConflict.
func (s *Syncer) Merge(tag string) (Result, error) {
	// The sync stages everything and drops a failed adaptation's changes with
	// git clean: in a working tree with changes, it would take or delete them.
	status, err := s.Repo.Run("status", "--porcelain")
	if err != nil {
		return Result{}, err
	}
	if status != "" {
		return Result{}, errors.New("the working tree has changes: commit or stash them first")
	}
	err = s.fetchUpstreamTags()
	if err != nil {
		return Result{}, err
	}
	err = s.checkTag(tag)
	if err != nil {
		return Result{}, err
	}
	ref := upstreamRefs + tag
	base, err := s.Repo.Run("rev-parse", "HEAD")
	if err != nil {
		return Result{}, err
	}
	synced, err := s.lastSyncedTag()
	if err != nil {
		return Result{}, err
	}
	res := Result{Tag: tag, Branch: BranchPrefix + tag, Base: strings.TrimSpace(base), Synced: synced}

	owned, err := s.ownedChangedUpstream(res.Base, ref)
	if err != nil {
		return Result{}, err
	}
	s.Logger.Info("Merging the upstream release", "tag", tag, "branch", res.Branch, "forkOwnedChangedUpstream", owned)
	_, err = s.Repo.Run("checkout", "--quiet", "-b", res.Branch)
	if err != nil {
		return Result{}, err
	}

	conflicts, err := s.mergeUpstreamChanges(ref, synced, owned)
	if err != nil {
		return Result{}, err
	}
	if len(conflicts) > 0 {
		s.Logger.Info("The merge has conflicts, committing it as it is", "files", conflicts)
		res.Status = StatusConflict
		res.Conflicts = conflicts
		res.Restored, err = s.commitConflicts(tag, res.Base, owned, conflicts)
		if err != nil {
			return Result{}, err
		}
		return res, nil
	}

	_, err = s.Repo.Run("commit", "--quiet", "-m", "chore: merge upstream tag "+tag)
	if err != nil {
		return Result{}, err
	}
	err = s.commitAdaptations(&res, owned)
	if err != nil {
		return Result{}, err
	}
	res.Status = StatusMerged
	s.Logger.Info("Merged the upstream release", "tag", tag, "failedAdaptations", res.FailedAdaptations)
	return res, nil
}

func (s *Syncer) fetchUpstreamTags() error {
	s.Logger.V(1).Info("Fetching upstream's tags", "upstream", s.Config.Upstream)
	_, err := s.Repo.Run("fetch", "--quiet", "--no-tags", s.Config.Upstream, "+refs/tags/*:"+upstreamRefs+"*")
	return err
}

// latestStableTag returns upstream's latest stable release.
func (s *Syncer) latestStableTag() (string, error) {
	out, err := s.Repo.Run("for-each-ref", "--format=%(refname:strip=2)", upstreamRefs)
	if err != nil {
		return "", err
	}
	latest := latestStable(out)
	if latest == "" {
		return "", fmt.Errorf("no stable release tags found at %s", s.Config.Upstream)
	}
	return latest, nil
}

// lastSyncedTag returns the latest stable upstream release the fork has: the
// one the last sync merged, or "" if there is none.
func (s *Syncer) lastSyncedTag() (string, error) {
	out, err := s.Repo.Run("for-each-ref", "--merged", "HEAD", "--format=%(refname:strip=2)", upstreamRefs)
	if err != nil {
		return "", err
	}
	return latestStable(out), nil
}

// latestStable returns the latest stable release among tags, or "" if there is
// none.
func latestStable(tags string) string {
	latest := ""
	for tag := range strings.FieldsSeq(tags) {
		if stableTag.MatchString(tag) && (latest == "" || semver.Compare(tag, latest) > 0) {
			latest = tag
		}
	}
	return latest
}

func (s *Syncer) checkTag(tag string) error {
	if !stableTag.MatchString(tag) {
		return fmt.Errorf("%q: %w", tag, ErrInvalidTag)
	}
	if !s.Repo.Succeeds("rev-parse", "--verify", "--quiet", upstreamRefs+tag) {
		return fmt.Errorf("upstream tag %q not found", tag)
	}
	// The fork only moves forward: merging an older release on top of a newer
	// one would undo the newer one's changes where they conflict.
	synced, err := s.lastSyncedTag()
	if err != nil {
		return err
	}
	if synced != "" && semver.Compare(tag, synced) < 0 {
		return fmt.Errorf("upstream tag %s is older than %s, which the fork already has", tag, synced)
	}
	return nil
}

// ownedChangedUpstream returns the files under the fork-owned paths that
// upstream added, changed, deleted or renamed (both names) since the last sync,
// except the files that follow upstream.
func (s *Syncer) ownedChangedUpstream(head, ref string) ([]string, error) {
	if len(s.Config.OwnedPaths) == 0 {
		return nil, nil
	}
	mergeBase, err := s.Repo.Run("merge-base", head, ref)
	if err != nil {
		return nil, err
	}
	diff := []string{"diff", "--name-only", "--no-renames", "-z", strings.TrimSpace(mergeBase), ref, "--"}
	return s.Repo.Paths(slices.Concat(diff, s.Config.OwnedPaths)...)
}

// mergeUpstreamChanges merges ref without committing, with the fork-owned paths
// set to upstream's version, and returns the files that still conflict. synced
// is the last upstream release the fork has, or "" if it has none.
func (s *Syncer) mergeUpstreamChanges(ref, synced string, owned []string) ([]string, error) {
	_, mergeErr := s.Repo.Run("merge", "--no-ff", "--no-commit", ref) // fails on conflicts, which are handled below
	if !s.Repo.Succeeds("rev-parse", "--quiet", "--verify", "MERGE_HEAD") {
		if mergeErr == nil {
			return nil, fmt.Errorf("nothing to merge from %s", ref)
		}
		return nil, fmt.Errorf("failed to merge %s: %w", ref, mergeErr)
	}
	err := s.takePathsFrom(ref, owned)
	if err != nil {
		return nil, err
	}
	err = s.keepOtherOwnedFiles(owned)
	if err != nil {
		return nil, err
	}
	conflicts, err := s.Repo.Paths("diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil {
		return nil, err
	}
	if len(conflicts) == 0 {
		return nil, nil
	}
	return s.resolveConflicts(ref, synced, conflicts)
}

// keepOtherOwnedFiles puts back the fork's version of the fork-owned files
// that the merge changed although upstream did not change them (owned lists the
// ones it did). git's rename detection does that: when the fork replaced an
// upstream file with a renamed copy, such as a regenerated CRD, upstream's
// changes to the original are applied to the copy.
func (s *Syncer) keepOtherOwnedFiles(owned []string) error {
	if len(s.Config.OwnedPaths) == 0 {
		return nil
	}
	changed, err := s.Repo.Paths(slices.Concat([]string{"diff", "--name-only", "--no-renames", "-z", "HEAD", "--"}, s.Config.OwnedPaths)...)
	if err != nil {
		return err
	}
	var others []string
	for _, file := range changed {
		if !slices.Contains(owned, file) {
			others = append(others, file)
		}
	}
	return s.takePathsFrom("HEAD", others)
}

// resolveConflicts merges each conflicting file again, from the last upstream
// release the fork has rather than from the base git picked, and returns the
// files that still conflict.
//
// git merges from the commit where the two histories meet, which can be older
// than the last sync: upstream releases patches from release branches, so a
// fork that synced v1.9.3 (on release/v1.9.x) meets v1.10.0 (on main) at
// v1.9.0, and every change the patches share with main conflicts. The rename is
// applied to both upstream versions, so that upstream changes next to a renamed
// line do not conflict either.
func (s *Syncer) resolveConflicts(ref, synced string, conflicts []string) ([]string, error) {
	if synced == "" {
		return conflicts, nil
	}
	renamedFiles, err := s.renamedFiles()
	if err != nil {
		return nil, err
	}

	var remaining []string
	for _, file := range conflicts {
		resolved, err := s.mergeFile(file, upstreamRefs+synced, ref, renamedFiles[file])
		if err != nil {
			return nil, err
		}
		if !resolved {
			remaining = append(remaining, file)
		}
	}
	s.Logger.Info("Merged the conflicting files again from the last synced release",
		"release", synced, "conflicts", len(conflicts), "stillConflicting", remaining)
	return remaining, nil
}

// renamedFiles returns the files whose content the rename renames in place.
func (s *Syncer) renamedFiles() (map[string]bool, error) {
	renamed := map[string]bool{}
	if s.Config.Rename == nil {
		return renamed, nil
	}
	files, err := rename.Files(s.Repo, *s.Config.Rename, s.Config.OwnedPaths)
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		renamed[file] = rename.Target(*s.Config.Rename, file) == file
	}
	return renamed, nil
}

// mergeFile merges the fork's version of file with upstream's at theirs, from
// upstream's at base, with the rename applied to both upstream versions if
// renamed is true. A clean result is written to the working tree and staged,
// and true is returned.
func (s *Syncer) mergeFile(file, base, theirs string, renamed bool) (bool, error) {
	for _, ref := range []string{"HEAD", base, theirs} {
		if !s.Repo.Succeeds("cat-file", "-e", ref+":"+file) {
			return false, nil // added or deleted on one side: left for a person
		}
	}

	dir, err := os.MkdirTemp("", "fork-sync-")
	if err != nil {
		return false, err
	}
	defer func() {
		_ = os.RemoveAll(dir) // a temporary directory: nothing to do if it stays
	}()

	oursFile, err := s.writeVersion(dir, "ours", "HEAD", file, false)
	if err != nil {
		return false, err
	}
	baseFile, err := s.writeVersion(dir, "base", base, file, renamed)
	if err != nil {
		return false, err
	}
	theirsFile, err := s.writeVersion(dir, "theirs", theirs, file, renamed)
	if err != nil {
		return false, err
	}

	// merge-file writes the result to its first file, and fails on conflicts.
	if !s.Repo.Succeeds("merge-file", oursFile, baseFile, theirsFile) {
		return false, nil
	}
	merged, err := os.ReadFile(oursFile)
	if err != nil {
		return false, err
	}
	err = os.WriteFile(filepath.Join(s.Repo.Dir, file), merged, 0o644)
	if err != nil {
		return false, err
	}
	_, err = s.Repo.Run("add", "--", file)
	if err != nil {
		return false, err
	}
	return true, nil
}

// writeVersion writes file as it is at ref to dir/name, renamed if renamed is
// true, and returns its path.
func (s *Syncer) writeVersion(dir, name, ref, file string, renamed bool) (string, error) {
	content, err := s.Repo.Run("show", ref+":"+file)
	if err != nil {
		return "", err
	}
	if renamed {
		content = rename.Content(*s.Config.Rename, content)
	}
	path := filepath.Join(dir, name)
	err = os.WriteFile(path, []byte(content), 0o644)
	if err != nil {
		return "", err
	}
	return path, nil
}

// commitConflicts commits the unresolved merge, markers and list of conflicting
// files included, then puts the fork-owned paths back in a commit of their own.
func (s *Syncer) commitConflicts(tag, base string, owned, conflicts []string) ([]git.Change, error) {
	list := strings.Join(conflicts, "\n") + "\n"
	err := os.WriteFile(filepath.Join(s.Repo.Dir, ConflictsFile), []byte(list), 0o644)
	if err != nil {
		return nil, err
	}
	_, err = s.Repo.Run("add", "--all")
	if err != nil {
		return nil, err
	}
	_, err = s.Repo.Run("commit", "--quiet", "--no-verify", "-m", "chore: merge upstream tag "+tag+" (unresolved conflicts)")
	if err != nil {
		return nil, err
	}

	restored, err := s.restoreOwned(base, owned)
	if err != nil || len(restored) == 0 {
		return restored, err
	}
	_, err = s.Repo.Run("commit", "--quiet", "--no-verify",
		"-m", "chore: keep the fork-owned paths after merging "+tag,
		"-m", "The merge has conflicts, so only the fork-owned paths are put back for now "+
			"(status is relative to the merge commit):\n"+formatChanges(restored))
	return restored, err
}

// Rename applies the configured rename to the working tree, leaving out the
// fork-owned paths, and returns the paths it wrote. Without a rename configured
// it does nothing.
func (s *Syncer) Rename() ([]string, error) {
	if s.Config.Rename == nil {
		return nil, nil
	}
	return rename.Apply(s.Repo, *s.Config.Rename, s.Config.OwnedPaths)
}

// commitAdaptations puts the fork-owned paths back, renames and runs the
// adaptations, then commits all of it, if anything changed.
func (s *Syncer) commitAdaptations(res *Result, owned []string) error {
	var err error
	res.Restored, err = s.restoreOwned(res.Base, owned)
	if err != nil {
		return err
	}
	_, err = s.Rename()
	if err != nil {
		return fmt.Errorf("failed to rename: %w", err)
	}
	// Stage everything, so that a failed adaptation's changes are all that is
	// left unstaged, and can be dropped.
	_, err = s.Repo.Run("add", "--all")
	if err != nil {
		return err
	}
	for _, adaptation := range s.Config.Adaptations {
		err = s.runAdaptation(adaptation, res)
		if err != nil {
			return err
		}
	}

	if s.Repo.Succeeds("diff", "--cached", "--quiet") {
		return nil
	}
	_, err = s.Repo.Run("commit", "--quiet",
		"-m", "chore: apply Qdrant adaptations after merging "+res.Tag,
		"-m", adaptationsMessage(*res))
	return err
}

// runAdaptation runs an adaptation and stages what it changed. If it fails and
// is allowed to, its changes are dropped and it is added to
// res.FailedAdaptations instead. Everything before it must be staged.
func (s *Syncer) runAdaptation(adaptation config.Adaptation, res *Result) error {
	runErr := s.run(adaptation)
	if runErr == nil {
		// Adaptations run upstream's code (make runs its Makefile).
		changedWorkflows, err := s.Repo.Paths("ls-files", "--modified", "--deleted", "--others", "--exclude-standard", "-z", "--", workflowsDir)
		if err != nil {
			return err
		}
		if len(changedWorkflows) > 0 {
			return fmt.Errorf("the adaptation %q changed workflows, which a sync never does: %s", adaptation.Name, strings.Join(changedWorkflows, ", "))
		}
		s.Logger.Info("Adaptation done", "adaptation", adaptation.Name)
		_, err = s.Repo.Run("add", "--all")
		return err
	}
	if !adaptation.AllowFailure {
		return fmt.Errorf("failed to run the adaptation %q: %w", adaptation.Name, runErr)
	}
	s.Logger.Error(runErr, "Adaptation failed, dropping its changes", "adaptation", adaptation.Name)
	res.FailedAdaptations = append(res.FailedAdaptations, adaptation.Name)
	return s.dropUnstaged()
}

// restoreOwned puts the given fork-owned paths back as they are in base, and
// returns their status relative to the current commit.
func (s *Syncer) restoreOwned(base string, owned []string) ([]git.Change, error) {
	err := s.takePathsFrom(base, owned)
	if err != nil {
		return nil, err
	}
	return s.Repo.NameStatus("--cached")
}

// takePathsFrom makes the working tree and index have each path as it is in
// ref, or not have it at all if ref does not.
func (s *Syncer) takePathsFrom(ref string, paths []string) error {
	for _, path := range paths {
		var err error
		if s.Repo.Succeeds("cat-file", "-e", ref+":"+path) {
			_, err = s.Repo.Run("checkout", ref, "--", path)
		} else {
			_, err = s.Repo.Run("rm", "-q", "-f", "--ignore-unmatch", "--", path)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// dropUnstaged reverts the changes that are not staged: changes to tracked
// files, and new untracked files. Ignored files are left alone.
func (s *Syncer) dropUnstaged() error {
	_, err := s.Repo.Run("checkout", "--", ".")
	if err != nil {
		return err
	}
	_, err = s.Repo.Run("clean", "-d", "--force", "--quiet")
	return err
}

// run runs the program of an adaptation in the repository. Its output goes where
// git's does.
func (s *Syncer) run(adaptation config.Adaptation) error {
	cmd := exec.Command(adaptation.Run[0], adaptation.Run[1:]...) //nolint:gosec // the commands come from the fork's own configuration
	cmd.Dir = s.Repo.Dir
	cmd.Stdout = s.Repo.Output
	cmd.Stderr = s.Repo.Output
	return cmd.Run()
}
