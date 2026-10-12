package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/tsbkw/agentlens/internal/ansi"
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
// Without --since, the last 7 days are reported (report.ParseWindow).
func ParseReportOptions(args []string, now time.Time) (ReportOptions, error) {
	opts := ReportOptions{Top: 10}
	var since, until string
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
	if opts.Window, err = report.ParseWindow(since, until, now); err != nil {
		return opts, fmt.Errorf("%w. %s", err, reportUsage)
	}
	return opts, nil
}

// CmdReport aggregates the anomalies of all sessions with calls in a time window.
func (a *App) CmdReport(args []string) error {
	opts, err := ParseReportOptions(args, time.Now())
	if err != nil {
		return err
	}
	r, err := report.Collect(a.candidates(), opts.Window, opts.Project, func(path string, err error) {
		fmt.Fprintf(os.Stderr, "skipping %s: %v\n", path, err)
	})
	if err != nil {
		return err
	}
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
	fmt.Fprintf(w, "\n%s📊 AgentLens Report:%s %s → %s (%s)\n", ansi.Bold, ansi.Reset, since, until, r.Window.Since.Format("MST"))
	fmt.Fprintln(w, strings.Repeat("─", 65))
	t := r.Total
	fmt.Fprintf(w, "Sessions: %d | Calls: %d | Failed: %d | Denied: %d\n", r.Sessions, t.Calls, t.Failed, t.Denied)
	fmt.Fprintf(w, "Anomalies: %sauth expired %d%s | %ssilent fallbacks %d%s (+%d possible, low confidence) | retry loops %d\n",
		ansi.Red, t.AuthExpired, ansi.Reset, ansi.Yellow, t.SilentFallbacks, ansi.Reset, t.PossibleFallbacks, t.RetryLoops)
	if r.Sessions == 0 {
		fmt.Fprintln(w, "\nNo tool calls recorded in this period.")
		return
	}

	table := func(title, keyHeader string, rows []report.Row, withProject bool) {
		if len(rows) == 0 {
			return
		}
		fmt.Fprintf(w, "\n%s%s%s\n", ansi.Bold, title, ansi.Reset)
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
				// The end of a project path tells projects apart; keep it
				line += "\t" + textutil.TruncateHead(row.Project, 40, "…")
			}
			fmt.Fprintln(tw, line)
		}
		tw.Flush()
		if len(rows) > top {
			fmt.Fprintf(w, "  %s… %d more (use --top or --json)%s\n", ansi.Dim, len(rows)-top, ansi.Reset)
		}
	}
	table("MCP servers", "SERVER", r.MCPServers, false)
	table("Tools", "TOOL", r.Tools, false)
	table("Sessions", "SESSION", r.BySession, true)
	renderDormant(w, r, top, reportNow())

	fmt.Fprintf(w, "\n%s💡 Tip:%s Use `agentlens graph <session-id>` to see the anomalies of a session.\n\n", ansi.Cyan, ansi.Reset)
}

// reportNow is the clock used to say how long a tool has been silent; tests replace it.
var reportNow = time.Now

// reasonLabels are the short forms shown in the dormant tools table.
var reasonLabels = map[string]string{
	report.ReasonConnectionFailing: "server fails to connect",
	report.ReasonPendingSignIn:     "waiting for sign-in",
	report.ReasonAuthUnresolved:    "auth failed, not fixed",
	report.ReasonDenied:            "denied",
	report.ReasonReplaced:          "replaced by a substitute",
	report.ReasonFailing:           "failing",
	report.ReasonUnused:            "offered, never called",
}

// ReasonLabel is the short description of a dormancy reason.
func ReasonLabel(reason string) string {
	if label, ok := reasonLabels[reason]; ok {
		return label
	}
	return reason
}

// renderDormant lists the Skills and MCP servers that are not working as of their last use,
// and names the ones offered but never called.
func renderDormant(w io.Writer, r *report.Report, top int, now time.Time) {
	dormant := r.Dormant()
	unused := r.Unused()
	if len(dormant) == 0 && len(unused) == 0 {
		return
	}
	if len(dormant) > 0 {
		fmt.Fprintf(w, "\n%s%s⚠️  Dormant Skills & MCP servers%s (not working as of their last use)\n", ansi.Bold, ansi.Yellow, ansi.Reset)
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  NAME\tKIND\tLAST SUCCESS\tSILENT FOR\tREASON\tLAST ERROR")
		for i, h := range dormant {
			if i == top {
				break
			}
			lastSuccess, silent := "never", fmt.Sprintf("%d sessions", h.SessionsSinceLastSuccess)
			if !h.LastSuccess.IsZero() {
				lastSuccess = h.LastSuccess.Local().Format("2006-01-02")
				silent = fmt.Sprintf("%s, %d sessions", textutil.FormatAge(now.Sub(h.LastSuccess)), h.SessionsSinceLastSuccess)
			}
			lastErr := h.LastError
			if h.Reason == report.ReasonConnectionFailing {
				lastErr = h.LastConnectError
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\n", textutil.Truncate(h.Key, 40, "…"), kindLabel(h.Kind), lastSuccess, silent,
				ReasonLabel(h.Reason), textutil.Truncate(lastErr, 60, "…"))
		}
		tw.Flush()
		if len(dormant) > top {
			fmt.Fprintf(w, "  %s… %d more (use --top or --json)%s\n", ansi.Dim, len(dormant)-top, ansi.Reset)
		}
		fmt.Fprintf(w, "  %sRun `agentlens why <name>` for the timeline and a diagnosis.%s\n", ansi.Dim, ansi.Reset)
	}
	if len(unused) > 0 {
		byKind := map[string][]string{}
		for _, h := range unused {
			byKind[h.Kind] = append(byKind[h.Kind], h.Key)
		}
		fmt.Fprintln(w)
		for _, kind := range []string{report.KindMCPServer, report.KindSkill} {
			names := byKind[kind]
			if len(names) == 0 {
				continue
			}
			const shown = 8
			more := ""
			if len(names) > shown {
				more = fmt.Sprintf(", … %d more (use --json)", len(names)-shown)
				names = names[:shown]
			}
			label := "MCP servers"
			if kind == report.KindSkill {
				label = "Skills"
			}
			fmt.Fprintf(w, "%s%s offered but never called (%d):%s %s%s\n", ansi.Dim, label, len(byKind[kind]), ansi.Reset, strings.Join(names, ", "), more)
		}
	}
}

func kindLabel(kind string) string {
	switch kind {
	case report.KindMCPServer:
		return "MCP"
	case report.KindSkill:
		return "Skill"
	}
	return kind
}
