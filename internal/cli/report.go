package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/tsbkw/agentlens/internal/collector"
	"github.com/tsbkw/agentlens/internal/detector"
	"github.com/tsbkw/agentlens/internal/graph"
	"github.com/tsbkw/agentlens/internal/report"
	"github.com/tsbkw/agentlens/internal/textutil"
)

// ReportOptions are the flags of `agentlens report`.
type ReportOptions struct {
	Window  report.Window
	Project string // substring of the session's project directory
	JSON    bool
	Top     int
}

const reportUsage = "Usage: agentlens report [--since <date|Nd>] [--until <date>] [--project <name>] [--top <n>] [--json]"

// ParseReportOptions parses report flags. Dates are YYYY-MM-DD in local time or RFC 3339;
// --since also accepts "<N>d" (N days ago). --until with a date includes that whole day.
// Without --since, the last 7 days are reported.
func ParseReportOptions(args []string, now time.Time) (ReportOptions, error) {
	opts := ReportOptions{Top: 10}
	since, until := "7d", ""
	for i := 0; i < len(args); i++ {
		flag, value, hasValue := strings.Cut(args[i], "=")
		next := func() (string, error) {
			if hasValue {
				return value, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value. %s", flag, reportUsage)
			}
			i++
			return args[i], nil
		}
		var err error
		switch flag {
		case "--since":
			since, err = next()
		case "--until":
			until, err = next()
		case "--project":
			opts.Project, err = next()
		case "--top":
			var v string
			if v, err = next(); err == nil {
				if opts.Top, err = strconv.Atoi(v); err != nil || opts.Top <= 0 {
					err = fmt.Errorf("--top must be a positive number")
				}
			}
		case "--json":
			opts.JSON = true
		default:
			err = fmt.Errorf("unknown report flag %q. %s", args[i], reportUsage)
		}
		if err != nil {
			return opts, err
		}
	}

	var err error
	if opts.Window.Since, err = parseReportTime(since, now, false); err != nil {
		return opts, fmt.Errorf("invalid --since: %w", err)
	}
	if until != "" {
		if opts.Window.Until, err = parseReportTime(until, now, true); err != nil {
			return opts, fmt.Errorf("invalid --until: %w", err)
		}
	}
	if !opts.Window.Until.IsZero() && !opts.Window.Since.Before(opts.Window.Until) {
		return opts, fmt.Errorf("--since must be before --until")
	}
	return opts, nil
}

func parseReportTime(s string, now time.Time, endOfDay bool) (time.Time, error) {
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

// CmdReport aggregates the anomalies of all sessions with calls in a time window.
func (a *App) CmdReport(args []string) error {
	opts, err := ParseReportOptions(args, time.Now())
	if err != nil {
		return err
	}
	sessions, err := collector.DiscoverAllSessions(a.candidates())
	if err != nil {
		return err
	}

	b := report.NewBuilder(opts.Window)
	for i := range sessions {
		s := &sessions[i]
		project := filepath.Base(filepath.Dir(s.FilePath))
		// A trace last written before the window cannot hold calls in it
		if s.ModTime.Before(opts.Window.Since) {
			continue
		}
		if opts.Project != "" && project != "logs" && !strings.Contains(project, opts.Project) {
			continue
		}
		provider := a.providerByID(s.ProviderID)
		if provider == nil {
			continue
		}
		data, err := collector.NewCollector(provider).IngestSessionFile(s.FilePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skipping %s: %v\n", s.FilePath, err)
			continue
		}
		if project == "logs" {
			for _, n := range data.Nodes {
				if cwd, ok := n.Arguments["Cwd"].(string); ok && cwd != "" {
					cwd = strings.Trim(cwd, "\"'")
					if base := filepath.Base(cwd); base != "" && base != "." && base != "/" {
						project = base
						break
					}
				}
			}
		}
		if opts.Project != "" && !strings.Contains(project, opts.Project) {
			continue
		}
		g := graph.NewGraphBuilder().BuildWithTurns(s.SessionID, provider.Definition.Provider.ID, data.Turns, data.Nodes)
		detector.NewDetector(provider).Analyze(g)
		b.Add(s.SessionID, project, data.Nodes, g)
	}

	r := b.Report()
	if opts.JSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	RenderReport(os.Stdout, r, opts.Top)
	return nil
}

// RenderReport prints the aggregate as tables, keeping the top rows of each.
func RenderReport(w io.Writer, r *report.Report, top int) {
	since, until := "the beginning", "now"
	if !r.Window.Since.IsZero() {
		since = r.Window.Since.Format("2006-01-02 15:04")
	}
	if !r.Window.Until.IsZero() {
		until = r.Window.Until.Format("2006-01-02 15:04")
	}
	fmt.Fprintf(w, "\n%s📊 AgentLens Report:%s %s → %s (%s)\n", colorBold, colorReset, since, until, r.Window.Since.Format("MST"))
	fmt.Fprintln(w, strings.Repeat("─", 65))
	t := r.Total
	fmt.Fprintf(w, "Sessions: %d | Calls: %d | Failed: %d | Denied: %d\n", r.Sessions, t.Calls, t.Failed, t.Denied)
	fmt.Fprintf(w, "Anomalies: %sauth expired %d%s | %ssilent fallbacks %d%s (+%d possible, low confidence) | retry loops %d\n",
		colorRed, t.AuthExpired, colorReset, colorYellow, t.SilentFallbacks, colorReset, t.PossibleFallbacks, t.RetryLoops)
	if r.Sessions == 0 {
		fmt.Fprintln(w, "\nNo tool calls recorded in this period.")
		return
	}

	table := func(title, keyHeader string, rows []report.Row, withProject bool) {
		if len(rows) == 0 {
			return
		}
		fmt.Fprintf(w, "\n%s%s%s\n", colorBold, title, colorReset)
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		header := keyHeader + "\tCALLS\tFAILED\tDENIED\tAUTH\tFALLBACK\tPOSSIBLE\tLOOPS"
		if withProject {
			header += "\tPROJECT"
		}
		fmt.Fprintln(tw, "  "+header)
		for i, row := range rows {
			if i == top {
				break
			}
			c := row.Counts
			line := fmt.Sprintf("  %s\t%d\t%d\t%d\t%d\t%d\t%d\t%d", textutil.Truncate(row.Key, 48, "…"),
				c.Calls, c.Failed, c.Denied, c.AuthExpired, c.SilentFallbacks, c.PossibleFallbacks, c.RetryLoops)
			if withProject {
				line += "\t" + textutil.Truncate(row.Project, 40, "…")
			}
			fmt.Fprintln(tw, line)
		}
		tw.Flush()
		if len(rows) > top {
			fmt.Fprintf(w, "  %s… %d more (use --top or --json)%s\n", colorDim, len(rows)-top, colorReset)
		}
	}
	table("MCP servers", "SERVER", r.MCPServers, false)
	table("Tools", "TOOL", r.Tools, false)
	table("Sessions", "SESSION", r.BySession, true)

	fmt.Fprintf(w, "\n%s💡 Tip:%s Use `agentlens graph <session-id>` to see the anomalies of a session.\n\n", colorCyan, colorReset)
}
