package history

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"importstats/internal/analyzer"
)

func TestRunSeveralCommitsOldestFirst(t *testing.T) {
	repo := newRepo(t)
	c1 := commitFile(t, repo, "a.txt", "a", "2024-01-01T10:00:00Z")
	c2 := commitFile(t, repo, "b.txt", "b", "2024-01-02T10:00:00Z")
	c3 := commitFile(t, repo, "c.txt", "c", "2024-01-03T10:00:00Z")

	rep, err := Run(Options{Repo: repo, Analyze: reportStub(t, nil, nil), Commits: 3, Every: "commit", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	got := commits(rep.Points)
	want := []string{c1, c2, c3}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commits = %v, want %v", got, want)
	}
	assertCleanWorktrees(t, repo)
}

func TestRunRecordsAnalysisErrorAndContinues(t *testing.T) {
	repo := newRepo(t)
	c1 := commitFile(t, repo, "a.txt", "a", "2024-01-01T10:00:00Z")
	c2 := commitFile(t, repo, "b.txt", "b", "2024-01-02T10:00:00Z")
	c3 := commitFile(t, repo, "c.txt", "c", "2024-01-03T10:00:00Z")

	rep, err := Run(Options{Repo: repo, Analyze: reportStub(t, map[string]error{c2: errors.New("entry missing")}, nil), Commits: 3, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if rep.Points[0].Commit != c1 || rep.Points[1].Commit != c2 || rep.Points[2].Commit != c3 {
		t.Fatalf("unexpected point order: %v", commits(rep.Points))
	}
	if rep.Points[1].Err == "" || rep.Points[0].Err != "" || rep.Points[2].Err != "" {
		t.Fatalf("errors not recorded as expected: %+v", rep.Points)
	}
	assertCleanWorktrees(t, repo)
}

func TestRunDetectsPackageAddedAndRemoved(t *testing.T) {
	repo := newRepo(t)
	c1 := commitFile(t, repo, "a.txt", "a", "2024-01-01T10:00:00Z")
	c2 := commitFile(t, repo, "b.txt", "b", "2024-01-02T10:00:00Z")
	c3 := commitFile(t, repo, "c.txt", "c", "2024-01-03T10:00:00Z")
	packages := map[string][]string{
		c1: {"react"},
		c2: {"lodash", "react"},
		c3: {"lodash"},
	}

	rep, err := Run(Options{Repo: repo, Analyze: reportStub(t, nil, packages), Commits: 3, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := changeNames(rep.Added), []string{"lodash"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("added = %v, want %v", got, want)
	}
	if got, want := changeNames(rep.Removed), []string{"react"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("removed = %v, want %v", got, want)
	}
	if rep.Added[0].Commit != c2 || rep.Removed[0].Commit != c3 {
		t.Fatalf("changes attached to wrong commits: added=%+v removed=%+v", rep.Added, rep.Removed)
	}
}

func TestRunEmptyAndSingleCommitRepos(t *testing.T) {
	empty := newRepo(t)
	rep, err := Run(Options{Repo: empty, Analyze: reportStub(t, nil, nil), Commits: 5, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("empty Run() error = %v", err)
	}
	if len(rep.Points) != 0 {
		t.Fatalf("empty repo points = %d, want 0", len(rep.Points))
	}

	single := newRepo(t)
	c1 := commitFile(t, single, "a.txt", "a", "2024-01-01T10:00:00Z")
	rep, err = Run(Options{Repo: single, Analyze: reportStub(t, nil, nil), Commits: 5, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("single Run() error = %v", err)
	}
	if got := commits(rep.Points); !reflect.DeepEqual(got, []string{c1}) {
		t.Fatalf("single commits = %v, want %v", got, []string{c1})
	}
}

func TestRunNonGitDirectoryErrors(t *testing.T) {
	if _, err := Run(Options{Repo: t.TempDir(), Analyze: reportStub(t, nil, nil), Timeout: 5 * time.Second}); err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("Run() error = %v, want not a git repository", err)
	}
}

func TestRunDailyAndWeeklySampling(t *testing.T) {
	repo := newRepo(t)
	commitFile(t, repo, "a.txt", "a", "2024-01-01T09:00:00Z")
	commitFile(t, repo, "b.txt", "b", "2024-01-01T10:00:00Z")
	commitFile(t, repo, "c.txt", "c", "2024-01-03T09:00:00Z")
	latestDay2 := commitFile(t, repo, "d.txt", "d", "2024-01-03T10:00:00Z")
	commitFile(t, repo, "e.txt", "e", "2024-01-10T09:00:00Z")
	latestWeek2 := commitFile(t, repo, "f.txt", "f", "2024-01-10T10:00:00Z")

	daily, err := Run(Options{Repo: repo, Analyze: reportStub(t, nil, nil), Commits: 2, Every: "day", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("daily Run() error = %v", err)
	}
	if got, want := commits(daily.Points), []string{latestDay2, latestWeek2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("daily commits = %v, want %v", got, want)
	}

	weekly, err := Run(Options{Repo: repo, Analyze: reportStub(t, nil, nil), Commits: 2, Every: "week", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("weekly Run() error = %v", err)
	}
	if got, want := commits(weekly.Points), []string{latestDay2, latestWeek2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("weekly commits = %v, want %v", got, want)
	}
}

func TestWorktreeCleanupAfterFailingRun(t *testing.T) {
	repo := newRepo(t)
	commitFile(t, repo, "a.txt", "a", "2024-01-01T10:00:00Z")
	_, err := Run(Options{Repo: repo, Analyze: func(string) (*analyzer.Report, error) {
		panic("boom")
	}, Commits: 1, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("Run() should record panic as point error, got %v", err)
	}
	assertCleanWorktrees(t, repo)
}

func TestRunDeterministic(t *testing.T) {
	repo := newRepo(t)
	commitFile(t, repo, "a.txt", "a", "2024-01-01T10:00:00Z")
	commitFile(t, repo, "b.txt", "b", "2024-01-02T10:00:00Z")
	opts := Options{Repo: repo, Analyze: reportStub(t, nil, nil), Commits: 2, Timeout: 5 * time.Second}
	a, err := Run(opts)
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	b, err := Run(opts)
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	if !bytes.Equal(aj, bj) {
		t.Fatalf("runs differ:\n%s\n%s", aj, bj)
	}
}

func TestWriteText(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteText(&buf, &Report{}); err != nil {
		t.Fatalf("empty WriteText() error = %v", err)
	}
	if !strings.Contains(buf.String(), "No history points") {
		t.Fatalf("empty output = %q", buf.String())
	}

	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	rep := &Report{Repo: "repo", Points: []Point{{Commit: "abcdef123", Short: "abcdef1", When: now, Files: 1, Imports: 2, Packages: 1}}}
	buf.Reset()
	if err := WriteText(&buf, rep); err != nil {
		t.Fatalf("one-point WriteText() error = %v", err)
	}
	if out := buf.String(); !strings.Contains(out, "abcdef1") || !strings.Contains(out, "Added packages: none") {
		t.Fatalf("one-point output missing expected text:\n%s", out)
	}

	rep.Points = append(rep.Points, Point{Commit: "123456789", Short: "1234567", When: now.AddDate(0, 0, 1), Err: "entry missing"})
	buf.Reset()
	if err := WriteText(&buf, rep); err != nil {
		t.Fatalf("failed-point WriteText() error = %v", err)
	}
	if out := buf.String(); !strings.Contains(out, "error: entry missing") || !strings.Contains(out, "-") {
		t.Fatalf("failed-point output missing expected text:\n%s", out)
	}
}

func newRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	return dir
}

func commitFile(t *testing.T, repo, name, body, when string) string {
	t.Helper()
	path := filepath.Join(repo, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	runGit(t, repo, "add", name)
	runGitEnv(t, repo, []string{"GIT_AUTHOR_DATE=" + when, "GIT_COMMITTER_DATE=" + when}, "-c", "user.email=test@example.com", "-c", "user.name=Test User", "commit", "-q", "-m", "commit "+name)
	return strings.TrimSpace(runGitOutput(t, repo, "rev-parse", "HEAD"))
}

func reportStub(t *testing.T, failures map[string]error, packages map[string][]string) AnalyzeFunc {
	t.Helper()
	return func(worktree string) (*analyzer.Report, error) {
		sha := strings.TrimSpace(runGitOutput(t, worktree, "rev-parse", "HEAD"))
		if err := failures[sha]; err != nil {
			return nil, err
		}
		names := packages[sha]
		pkgs := make([]analyzer.PackageStat, 0, len(names))
		for _, name := range names {
			pkgs = append(pkgs, analyzer.PackageStat{Name: name})
		}
		return &analyzer.Report{
			Summary:  analyzer.Summary{FilesAnalyzed: len(names) + 1, TotalImports: len(names) * 2, UniquePackages: len(names), OrphanFiles: len(names) % 2, Cycles: len(names) / 2},
			Packages: pkgs,
		}, nil
	}
}

func commits(points []Point) []string {
	out := make([]string, len(points))
	for i, pt := range points {
		out[i] = pt.Commit
	}
	return out
}

func changeNames(changes []PackageChange) []string {
	out := make([]string, len(changes))
	for i, ch := range changes {
		out[i] = ch.Package
	}
	return out
}

func assertCleanWorktrees(t *testing.T, repo string) {
	t.Helper()
	out := runGitOutput(t, repo, "worktree", "list", "--porcelain")
	count := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("worktree list has %d worktrees, want 1:\n%s", count, out)
	}
	entries, err := os.ReadDir(filepath.Join(repo, ".git", "importstats-history-worktrees"))
	if err == nil && len(entries) != 0 {
		t.Fatalf("scratch worktree directory not empty: %v", entries)
	}
}

func runGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	runGitEnv(t, repo, nil, args...)
}

func runGitEnv(t *testing.T, repo string, env []string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func runGitOutput(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return string(out)
}
