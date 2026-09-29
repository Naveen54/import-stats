// Package history samples importstats reports across git history without
// mutating the user's current working tree.
package history

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"importstats/internal/analyzer"
)

// AnalyzeFunc analyses a checked-out worktree and returns the report.
// It is injected so this package stays decoupled from the analysis pipeline.
type AnalyzeFunc func(worktree string) (*analyzer.Report, error)

// Options configures a historical analysis.
type Options struct {
	Repo    string
	Analyze AnalyzeFunc
	Commits int
	Every   string
	Since   string
	Timeout time.Duration
}

// Point is one sampled commit.
type Point struct {
	Commit   string    `json:"commit"`
	Short    string    `json:"short"`
	When     time.Time `json:"when"`
	Subject  string    `json:"subject"`
	Author   string    `json:"author"`
	Files    int       `json:"files"`
	Imports  int       `json:"imports"`
	Packages int       `json:"packages"`
	Orphans  int       `json:"orphans"`
	Cycles   int       `json:"cycles"`
	Err      string    `json:"error,omitempty"`
}

// PackageChange records when a package appeared or disappeared in the sampled
// history window.
type PackageChange struct {
	Package string    `json:"package"`
	Commit  string    `json:"commit"`
	Short   string    `json:"short"`
	When    time.Time `json:"when"`
	Subject string    `json:"subject"`
}

// Report is the resulting time series, oldest first.
type Report struct {
	Repo    string          `json:"repo"`
	Points  []Point         `json:"points"`
	Added   []PackageChange `json:"added"`
	Removed []PackageChange `json:"removed"`
}

type commitInfo struct {
	sha     string
	when    time.Time
	subject string
	author  string
}

// Run samples git history using detached temporary worktrees. The main working
// tree is never checked out or modified; uncommitted user changes are isolated
// from the detached worktrees.
func Run(opts Options) (rep *Report, err error) {
	if opts.Analyze == nil {
		return nil, errors.New("history: Analyze function is required")
	}
	if opts.Commits <= 0 {
		opts.Commits = 10
	}
	if opts.Every == "" {
		opts.Every = "commit"
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	if _, err := exec.LookPath("git"); err != nil {
		return nil, fmt.Errorf("history: git executable not found: %w", err)
	}

	repo, err := filepath.Abs(opts.Repo)
	if err != nil {
		return nil, fmt.Errorf("history: resolve repo path: %w", err)
	}
	if err := ensureGitRepo(repo); err != nil {
		return nil, err
	}

	commonDir, err := gitOutput(context.Background(), repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, fmt.Errorf("history: locate git metadata: %w", err)
	}
	worktreeRoot := filepath.Join(strings.TrimSpace(commonDir), "importstats-history-worktrees")
	if err := os.MkdirAll(worktreeRoot, 0o755); err != nil {
		return nil, fmt.Errorf("history: create worktree scratch dir: %w", err)
	}

	var worktrees []string
	defer func() {
		cleanupWorktrees(repo, worktrees)
		_ = gitRun(context.Background(), repo, "worktree", "prune")
		if r := recover(); r != nil {
			panic(r)
		}
	}()

	commits, err := sampleCommits(repo, opts)
	if err != nil {
		return nil, err
	}
	rep = &Report{Repo: repo}
	packageSets := make([]map[string]bool, 0, len(commits))

	for _, c := range commits {
		pt := Point{Commit: c.sha, Short: shortSHA(c.sha), When: c.when, Subject: c.subject, Author: c.author}
		worktree, err := newWorktreePath(worktreeRoot)
		if err != nil {
			pt.Err = err.Error()
			rep.Points = append(rep.Points, pt)
			packageSets = append(packageSets, nil)
			continue
		}
		worktrees = append(worktrees, worktree)

		ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
		if err := gitRun(ctx, repo, "worktree", "add", "--detach", worktree, c.sha); err != nil {
			pt.Err = fmt.Sprintf("checkout failed: %v", err)
		} else {
			analysisRep, err := analyzeWithTimeout(opts.Analyze, worktree, opts.Timeout)
			if err != nil {
				pt.Err = err.Error()
			} else if analysisRep == nil {
				pt.Err = "analysis returned no report"
			} else {
				fillPoint(&pt, analysisRep)
				packageSets = append(packageSets, packageSet(analysisRep))
			}
		}
		cancel()
		if pt.Err != "" {
			packageSets = append(packageSets, nil)
		}
		rep.Points = append(rep.Points, pt)
		cleanupWorktrees(repo, []string{worktree})
	}

	buildPackageChanges(rep, packageSets)
	return rep, nil
}

func ensureGitRepo(repo string) error {
	out, err := gitOutput(context.Background(), repo, "rev-parse", "--is-inside-work-tree")
	if err != nil || strings.TrimSpace(out) != "true" {
		if err == nil {
			err = errors.New("not inside a work tree")
		}
		return fmt.Errorf("history: %s is not a git repository: %w", repo, err)
	}
	return nil
}

func sampleCommits(repo string, opts Options) ([]commitInfo, error) {
	if gitRun(context.Background(), repo, "rev-parse", "--verify", "HEAD") != nil {
		return nil, nil
	}
	args := []string{"log", "--format=%H%x00%cI%x00%an%x00%s%x00"}
	if opts.Since != "" {
		if gitRun(context.Background(), repo, "rev-parse", "--verify", opts.Since+"^{commit}") == nil {
			args = append(args, opts.Since+"..HEAD")
		} else {
			args = append(args, "--since="+opts.Since)
		}
	}
	if opts.Every == "commit" {
		args = append(args, fmt.Sprintf("-%d", opts.Commits))
	} else if opts.Every != "day" && opts.Every != "week" && opts.Every != "month" {
		return nil, fmt.Errorf("history: unsupported sampling interval %q", opts.Every)
	}

	out, err := gitOutput(context.Background(), repo, args...)
	if err != nil {
		return nil, fmt.Errorf("history: read git log: %w", err)
	}
	all, err := parseLog(out)
	if err != nil {
		return nil, err
	}

	var picked []commitInfo
	if opts.Every == "commit" {
		picked = all
	} else {
		seen := map[string]bool{}
		for _, c := range all {
			key := periodKey(c.when, opts.Every)
			if seen[key] {
				continue
			}
			seen[key] = true
			picked = append(picked, c)
			if len(picked) >= opts.Commits {
				break
			}
		}
	}

	sort.SliceStable(picked, func(i, j int) bool {
		if !picked[i].when.Equal(picked[j].when) {
			return picked[i].when.Before(picked[j].when)
		}
		return picked[i].sha < picked[j].sha
	})
	return picked, nil
}

func parseLog(out string) ([]commitInfo, error) {
	out = strings.TrimRight(out, "\x00\n")
	if out == "" {
		return nil, nil
	}
	fields := strings.Split(out, "\x00")
	if len(fields)%4 != 0 {
		return nil, fmt.Errorf("history: unexpected git log format")
	}
	commits := make([]commitInfo, 0, len(fields)/4)
	for i := 0; i < len(fields); i += 4 {
		sha := strings.TrimSpace(fields[i])
		when, err := time.Parse(time.RFC3339, strings.TrimSpace(fields[i+1]))
		if err != nil {
			return nil, fmt.Errorf("history: parse commit time for %s: %w", sha, err)
		}
		commits = append(commits, commitInfo{sha: sha, when: when, author: strings.TrimSpace(fields[i+2]), subject: fields[i+3]})
	}
	return commits, nil
}

func periodKey(t time.Time, every string) string {
	switch every {
	case "day":
		return t.Format("2006-01-02")
	case "week":
		y, w := t.ISOWeek()
		return fmt.Sprintf("%04d-W%02d", y, w)
	case "month":
		return t.Format("2006-01")
	default:
		return t.Format(time.RFC3339Nano)
	}
}

func newWorktreePath(root string) (string, error) {
	dir, err := os.MkdirTemp(root, "commit-")
	if err != nil {
		return "", fmt.Errorf("create worktree path: %w", err)
	}
	if err := os.Remove(dir); err != nil {
		return "", fmt.Errorf("prepare worktree path: %w", err)
	}
	return dir, nil
}

func analyzeWithTimeout(fn AnalyzeFunc, worktree string, timeout time.Duration) (rep *analyzer.Report, err error) {
	type result struct {
		rep *analyzer.Report
		err error
	}
	ch := make(chan result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- result{err: fmt.Errorf("analysis panicked: %v", r)}
			}
		}()
		r, e := fn(worktree)
		ch <- result{rep: r, err: e}
	}()
	select {
	case r := <-ch:
		return r.rep, r.err
	case <-time.After(timeout):
		return nil, fmt.Errorf("analysis timed out after %s", timeout)
	}
}

func fillPoint(pt *Point, rep *analyzer.Report) {
	s := rep.Summary
	pt.Files = s.FilesAnalyzed
	pt.Imports = s.TotalImports
	pt.Packages = s.UniquePackages
	pt.Orphans = s.OrphanFiles
	pt.Cycles = s.Cycles
}

func packageSet(rep *analyzer.Report) map[string]bool {
	set := map[string]bool{}
	for _, ps := range rep.Packages {
		if ps.DepType == "builtin" || ps.Name == "" {
			continue
		}
		set[ps.Name] = true
	}
	return set
}

func buildPackageChanges(rep *Report, sets []map[string]bool) {
	var prev map[string]bool
	for i, curr := range sets {
		if curr == nil {
			continue
		}
		if prev == nil {
			prev = curr
			continue
		}
		pt := rep.Points[i]
		for _, name := range sortedDifference(curr, prev) {
			rep.Added = append(rep.Added, PackageChange{Package: name, Commit: pt.Commit, Short: pt.Short, When: pt.When, Subject: pt.Subject})
		}
		for _, name := range sortedDifference(prev, curr) {
			rep.Removed = append(rep.Removed, PackageChange{Package: name, Commit: pt.Commit, Short: pt.Short, When: pt.When, Subject: pt.Subject})
		}
		prev = curr
	}
}

func sortedDifference(a, b map[string]bool) []string {
	var out []string
	for k := range a {
		if !b[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func cleanupWorktrees(repo string, worktrees []string) {
	for _, wt := range worktrees {
		if wt == "" {
			continue
		}
		_ = gitRun(context.Background(), repo, "worktree", "remove", "--force", wt)
		_ = os.RemoveAll(wt)
	}
}

func gitOutput(ctx context.Context, repo string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", commandError(err, stderr.String())
	}
	return stdout.String(), nil
}

func gitRun(ctx context.Context, repo string, args ...string) error {
	_, err := gitOutput(ctx, repo, args...)
	return err
}

func commandError(err error, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, stderr)
}

func shortSHA(sha string) string {
	if len(sha) <= 7 {
		return sha
	}
	return sha[:7]
}
