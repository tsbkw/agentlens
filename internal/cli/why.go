package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/tsbkw/agentlens/internal/ansi"
	"github.com/tsbkw/agentlens/internal/report"
	"github.com/tsbkw/agentlens/internal/textutil"
)

const whyUsage = "Usage: agentlens why <skill|mcp-server> [--since <date|Nd>] [--until <date>] [--project <name>] [--json] [--prompt]"

// WhyOptions are the flags of `agentlens why`.
type WhyOptions struct {
	Name   string
	Window report.Window
	// Project narrows the sessions to one project (substring of its directory name).
	Project string
	JSON    bool
	// Prompt prints only the prompt for an AI assistant, for piping into the clipboard.
	Prompt bool
}

// DefaultWhySince is the window of `agentlens why` when --since is not given.
const DefaultWhySince = "30d"

// ParseWhyOptions parses `why` arguments: the tool name, then report-style window flags.
func ParseWhyOptions(args []string, now time.Time) (WhyOptions, error) {
	var opts WhyOptions
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--prompt":
			opts.Prompt = true
		case a == "--json":
			opts.JSON = true
		case a == "--top" || strings.HasPrefix(a, "--top="):
			return opts, fmt.Errorf("unknown why flag %q. %s", a, whyUsage)
		case a == "--since" || a == "--until" || a == "--project":
			// A flag that takes the next argument as its value
			rest = append(rest, a)
			if i+1 < len(args) {
				i++
				rest = append(rest, args[i])
			}
		case opts.Name == "" && !strings.HasPrefix(a, "-"):
			opts.Name = a
		default:
			rest = append(rest, a)
		}
	}
	if opts.Name == "" {
		return opts, fmt.Errorf("missing Skill or MCP server name. %s", whyUsage)
	}
	hasSince := false
	for _, a := range rest {
		if a == "--since" || strings.HasPrefix(a, "--since=") {
			hasSince = true
		}
	}
	if !hasSince {
		rest = append(rest, "--since", DefaultWhySince)
	}
	ropts, err := ParseReportOptions(rest, now)
	if err != nil {
		return opts, fmt.Errorf("%s", strings.Replace(err.Error(), reportUsage, whyUsage, 1))
	}
	opts.Window, opts.Project = ropts.Window, ropts.Project
	return opts, nil
}

// CmdWhy explains why a Skill or MCP server is not being used.
func (a *App) CmdWhy(args []string) error {
	opts, err := ParseWhyOptions(args, time.Now())
	if err != nil {
		return err
	}
	ex, err := report.Explain(a.candidates(), opts.Window, opts.Project, opts.Name, func(path string, err error) {
		fmt.Fprintf(os.Stderr, "skipping %s: %v\n", path, err)
	})
	if err != nil {
		return err
	}
	switch {
	case opts.JSON:
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(ex)
	case opts.Prompt:
		fmt.Fprint(os.Stdout, ex.Prompt)
		return nil
	}
	RenderExplanation(os.Stdout, ex, reportNow())
	return nil
}

// RenderExplanation prints the diagnosis, its evidence, the session timeline, the
// substitutes and the prompt for an AI assistant.
func RenderExplanation(w io.Writer, ex *report.Explanation, now time.Time) {
	h := ex.Tool
	title := ex.Query
	if h.Key != "" {
		title = fmt.Sprintf("%s %s", kindLabel(h.Kind), h.Key)
	}
	since, until := "the beginning", "now"
	if !ex.Window.Since.IsZero() {
		since = ex.Window.Since.Local().Format("2006-01-02")
	}
	if !ex.Window.Until.IsZero() {
		until = ex.Window.Until.Local().Format("2006-01-02")
	}
	fmt.Fprintf(w, "\n%s🔎 Why is %s not used?%s  (%s → %s)\n", ansi.Bold, title, ansi.Reset, since, until)
	fmt.Fprintln(w, strings.Repeat("─", 65))

	d := ex.Diagnosis
	color := ansi.Yellow
	switch d.Reason {
	case report.DiagnosisHealthy:
		color = ansi.Green
	case report.DiagnosisNotFound, report.DiagnosisNoDemand:
		color = ansi.Dim
	case report.ReasonConnectionFailing, report.ReasonAuthUnresolved:
		color = ansi.Red
	}
	fmt.Fprintf(w, "%sDiagnosis:%s %s%s%s\n%s\n", ansi.Bold, ansi.Reset, color, report.ReasonTitle(d.Reason), ansi.Reset, d.Summary)
	if len(d.Evidence) > 0 {
		fmt.Fprintf(w, "\n%sEvidence%s\n", ansi.Bold, ansi.Reset)
		for _, e := range d.Evidence {
			fmt.Fprintf(w, "  • %s\n", e)
		}
	}

	if len(ex.Sessions) > 0 {
		const shown = 12
		start := 0
		if len(ex.Sessions) > shown {
			start = len(ex.Sessions) - shown
		}
		fmt.Fprintf(w, "\n%sSessions%s", ansi.Bold, ansi.Reset)
		if start > 0 {
			fmt.Fprintf(w, " %s(last %d of %d; --json has all)%s", ansi.Dim, shown, len(ex.Sessions), ansi.Reset)
		}
		fmt.Fprintln(w)
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  WHEN\tSESSION\tPROJECT\tOUTCOME\tDETAIL")
		for _, s := range ex.Sessions[start:] {
			detail := s.FirstError
			if s.ConnectError != "" {
				detail = s.ConnectError
			}
			if len(s.FallbackTo) > 0 {
				detail = strings.TrimSpace(detail + " → fell back to " + strings.Join(s.FallbackTo, ", "))
			}
			if s.SubstituteCalls > 0 && s.Calls == 0 {
				detail = strings.TrimSpace(fmt.Sprintf("%s %d substitute calls", detail, s.SubstituteCalls))
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n", s.Start.Local().Format("01-02 15:04"), textutil.Truncate(s.SessionID, 8, ""),
				textutil.TruncateHead(s.Project, 24, "…"), s.Outcome(), textutil.Truncate(detail, 70, "…"))
		}
		tw.Flush()
	}

	if len(ex.Substitutes) > 0 {
		fmt.Fprintf(w, "\n%sReached %s another way%s\n", ansi.Bold, ex.Service, ansi.Reset)
		for _, s := range ex.Substitutes {
			fmt.Fprintf(w, "  • %s in %d sessions (%d while %s was failing or down, %d while it worked, %d while it was not called): %s\n",
				s.Tool, s.Sessions, s.SessionsWhileFailing, h.Key, s.SessionsWhileHealthy, s.SessionsWhileAbsent, textutil.Truncate(s.Example, 70, "…"))
		}
	}

	fmt.Fprintf(w, "\n%sRecommendation:%s %s\n", ansi.Bold, ansi.Reset, d.Recommendation)
	if d.Reason != report.DiagnosisHealthy {
		fmt.Fprintf(w, "\n%s💬 To find the cause on this machine, paste this into a Claude Code session here (or run `agentlens why %s --prompt | pbcopy`):%s\n", ansi.Cyan, ex.Query, ansi.Reset)
		fmt.Fprintln(w, strings.Repeat("┄", 65))
		fmt.Fprint(w, ex.Prompt)
		fmt.Fprintln(w, strings.Repeat("┄", 65))
	}
	fmt.Fprintln(w)
}
