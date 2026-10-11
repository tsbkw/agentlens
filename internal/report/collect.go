package report

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tsbkw/agentlens/internal/collector"
	"github.com/tsbkw/agentlens/internal/detector"
	"github.com/tsbkw/agentlens/internal/graph"
	"github.com/tsbkw/agentlens/internal/providers"
)

// DefaultSince is the start of the window when none is given: the last 7 days.
const DefaultSince = "7d"

// ParseWindow parses the bounds of a window. Each is YYYY-MM-DD in now's location or RFC 3339;
// since also accepts "<N>d" (N days ago, at midnight). An until date includes that whole day.
// An empty since means DefaultSince; an empty until leaves the window open.
func ParseWindow(since, until string, now time.Time) (Window, error) {
	if since == "" {
		since = DefaultSince
	}
	var w Window
	var err error
	if w.Since, err = parseTime(since, now, false); err != nil {
		return w, fmt.Errorf("invalid since: %w", err)
	}
	if until != "" {
		if w.Until, err = parseTime(until, now, true); err != nil {
			return w, fmt.Errorf("invalid until: %w", err)
		}
		if !w.Since.Before(w.Until) {
			return w, fmt.Errorf("since must be before until")
		}
	}
	return w, nil
}

func parseTime(s string, now time.Time, endOfDay bool) (time.Time, error) {
	if days, ok := strings.CutSuffix(s, "d"); ok {
		if n, err := strconv.Atoi(days); err == nil && n >= 0 {
			y, m, d := now.Date()
			return time.Date(y, m, d, 0, 0, 0, 0, now.Location()).AddDate(0, 0, -n), nil
		}
	}
	if t, err := time.ParseInLocation("2006-01-02", s, now.Location()); err == nil {
		if endOfDay {
			t = t.AddDate(0, 0, 1)
		}
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("%q is not YYYY-MM-DD, RFC 3339 or <N>d", s)
}

// ProjectOf is the project a session trace belongs to: the name of its directory.
func ProjectOf(s collector.SessionInfo) string {
	return filepath.Base(filepath.Dir(s.FilePath))
}

// Collect analyzes the sessions discovered by provs whose project contains project (all when
// it is empty) and aggregates their calls in window. Traces that cannot be read are passed to
// skip, when set, and left out.
func Collect(provs []*providers.LoadedProvider, window Window, project string, skip func(path string, err error)) (*Report, error) {
	sessions, err := collector.DiscoverAllSessions(provs)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*providers.LoadedProvider, len(provs))
	for _, p := range provs {
		byID[p.Definition.Provider.ID] = p
	}

	b := NewBuilder(window)
	for _, s := range sessions {
		name := ProjectOf(s)
		// A trace last written before the window cannot hold calls in it
		if s.ModTime.Before(window.Since) || (project != "" && name != "logs" && !strings.Contains(name, project)) {
			continue
		}
		provider := byID[s.ProviderID]
		if provider == nil {
			continue
		}
		data, err := collector.NewCollector(provider).IngestSessionFile(s.FilePath)
		if err != nil {
			if skip != nil {
				skip(s.FilePath, err)
			}
			continue
		}
		if name == "logs" {
			for _, n := range data.Nodes {
				if cwd, ok := n.Arguments["Cwd"].(string); ok && cwd != "" {
					cwd = strings.Trim(cwd, "\"'")
					if base := filepath.Base(cwd); base != "" && base != "." && base != "/" {
						name = base
						break
					}
				}
			}
		}
		if project != "" && !strings.Contains(name, project) {
			continue
		}
		g := graph.NewGraphBuilder().BuildWithTurns(s.SessionID, provider.Definition.Provider.ID, data.Turns, data.Nodes)
		detector.NewDetector(provider).Analyze(g)
		b.Add(s.SessionID, name, data.Nodes, g)
	}
	return b.Report(), nil
}
