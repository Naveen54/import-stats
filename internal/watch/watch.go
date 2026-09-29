// Package watch polls source roots and reports debounced filesystem changes.
package watch

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Options configures a Watcher.
type Options struct {
	Roots    []string      // directories to watch (typically the src root)
	Exts     []string      // file extensions that matter, e.g. .js .jsx .ts .tsx
	Ignore   []string      // substrings/globs to skip (same semantics as analysis.Options.Ignore)
	Interval time.Duration // poll interval, default 1s
	Debounce time.Duration // quiet period before firing, default 300ms
}

// Event describes one debounced batch of changes.
type Event struct {
	Changed []string // sorted, absolute paths
	Added   []string
	Removed []string
	At      time.Time
}

// Watcher polls the filesystem and reports change batches.
type Watcher struct {
	roots    []string
	exts     map[string]struct{}
	ignore   []string
	interval time.Duration
	debounce time.Duration
	events   chan Event
}

type fileState struct {
	mod  time.Time
	size int64
}

var skippedDirs = map[string]struct{}{
	"node_modules": {},
	".git":         {},
	"dist":         {},
	"build":        {},
	"coverage":     {},
}

// New creates a filesystem polling watcher.
func New(opts Options) (*Watcher, error) {
	interval := opts.Interval
	if interval <= 0 {
		interval = time.Second
	}
	debounce := opts.Debounce
	if debounce <= 0 {
		debounce = 300 * time.Millisecond
	}

	roots := make([]string, 0, len(opts.Roots))
	for _, root := range opts.Roots {
		if root == "" {
			continue
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		roots = append(roots, filepath.Clean(abs))
	}
	sort.Strings(roots)

	exts := opts.Exts
	if len(exts) == 0 {
		exts = []string{".js", ".jsx", ".ts", ".tsx"}
	}
	extSet := make(map[string]struct{}, len(exts))
	for _, ext := range exts {
		if ext == "" {
			continue
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		extSet[strings.ToLower(ext)] = struct{}{}
	}

	ignore := append([]string(nil), opts.Ignore...)
	return &Watcher{
		roots:    roots,
		exts:     extSet,
		ignore:   ignore,
		interval: interval,
		debounce: debounce,
		events:   make(chan Event, 16),
	}, nil
}

// Events returns a channel of change batches. The channel is closed when the
// watcher stops.
func (w *Watcher) Events() <-chan Event { return w.events }

// Start begins polling until ctx is cancelled.
func (w *Watcher) Start(ctx context.Context) {
	defer close(w.events)

	prev := w.snapshot()
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	var pending Event
	var havePending bool
	var timer *time.Timer
	var timerC <-chan time.Time
	stopTimer := func() {
		if timer == nil {
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer = nil
		timerC = nil
	}
	armTimer := func() {
		if timer == nil {
			timer = time.NewTimer(w.debounce)
		} else {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(w.debounce)
		}
		timerC = timer.C
	}
	defer stopTimer()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			next := w.snapshot()
			delta := diff(prev, next)
			prev = next
			if delta.empty() {
				continue
			}
			pending.merge(delta)
			havePending = true
			armTimer()
		case <-timerC:
			if !havePending {
				timerC = nil
				continue
			}
			pending.finalize(time.Now())
			select {
			case w.events <- pending:
			case <-ctx.Done():
				return
			}
			pending = Event{}
			havePending = false
			timerC = nil
		}
	}
}

func (w *Watcher) snapshot() map[string]fileState {
	out := make(map[string]fileState)
	for _, root := range w.roots {
		info, err := os.Lstat(root)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if !info.IsDir() {
			if w.relevant(root) && !w.ignored(root) {
				out[root] = fileState{mod: info.ModTime(), size: info.Size()}
			}
			continue
		}
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if path != root && d.Type()&os.ModeSymlink != 0 {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				if path != root {
					if _, ok := skippedDirs[d.Name()]; ok || w.ignored(path) {
						return filepath.SkipDir
					}
				}
				return nil
			}
			if !w.relevant(path) || w.ignored(path) {
				return nil
			}
			info, err := d.Info()
			if err != nil || info.Mode()&os.ModeSymlink != 0 {
				return nil
			}
			out[path] = fileState{mod: info.ModTime(), size: info.Size()}
			return nil
		})
	}
	return out
}

func (w *Watcher) relevant(path string) bool {
	_, ok := w.exts[strings.ToLower(filepath.Ext(path))]
	return ok
}

func (w *Watcher) ignored(path string) bool {
	slash := filepath.ToSlash(path)
	for _, p := range w.ignore {
		if p == "" {
			continue
		}
		if strings.Contains(slash, p) {
			return true
		}
		if ok, _ := filepath.Match(p, filepath.Base(path)); ok {
			return true
		}
	}
	return false
}

func diff(prev, next map[string]fileState) Event {
	var ev Event
	for path, now := range next {
		old, ok := prev[path]
		if !ok {
			ev.Added = append(ev.Added, path)
			continue
		}
		if !old.mod.Equal(now.mod) || old.size != now.size {
			ev.Changed = append(ev.Changed, path)
		}
	}
	for path := range prev {
		if _, ok := next[path]; !ok {
			ev.Removed = append(ev.Removed, path)
		}
	}
	ev.sort()
	return ev
}

func (e Event) empty() bool {
	return len(e.Changed) == 0 && len(e.Added) == 0 && len(e.Removed) == 0
}

func (e *Event) merge(next Event) {
	e.Changed = append(e.Changed, next.Changed...)
	e.Added = append(e.Added, next.Added...)
	e.Removed = append(e.Removed, next.Removed...)
}

func (e *Event) finalize(at time.Time) {
	e.At = at
	e.Changed = uniq(e.Changed)
	e.Added = uniq(e.Added)
	e.Removed = uniq(e.Removed)
}

func (e *Event) sort() {
	sort.Strings(e.Changed)
	sort.Strings(e.Added)
	sort.Strings(e.Removed)
}

func uniq(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	sort.Strings(in)
	out := in[:0]
	var last string
	for i, v := range in {
		if i == 0 || v != last {
			out = append(out, v)
			last = v
		}
	}
	return out
}
