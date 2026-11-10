package lint

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// WriteText writes the report for a person reading a terminal.
func (r *Report) WriteText(w io.Writer) error {
	var b strings.Builder
	if len(r.Errors) > 0 {
		for _, f := range r.Errors {
			fmt.Fprintf(&b, "error: %s\n", f)
		}
		fmt.Fprintf(&b, "\n%s; the bundle is not valid, so nothing else was checked\n", count(len(r.Errors), "error"))
		_, err := io.WriteString(w, b.String())
		return err
	}

	fmt.Fprintf(&b, "%s\n", r.Title)
	fmt.Fprintf(&b, "%s..%s, %s, %s\n\n", short(r.Base), short(r.Head),
		count(len(r.Files), "file"), count(r.Coverage.Total, "changed line"))

	b.WriteString("Coverage\n")
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', tabwriter.AlignRight)
	c := r.Coverage
	for _, row := range []struct {
		name string
		n    int
	}{{"specific", c.Specific}, {"broad", c.Broad}, {"skim", c.Skim}, {"uncovered", c.Uncovered}} {
		fmt.Fprintf(tw, "  %s\t%d\t%s\t\n", row.name, row.n, percent(row.n, c.Total))
	}
	tw.Flush()

	b.WriteString("\nSections\n")
	tw = tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	for _, s := range r.Sections {
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", s.ID, s.Author, sectionSummary(s))
		for _, a := range s.Anchors {
			if a.State != "exact" {
				fmt.Fprintf(tw, "    %s\t\t%s\n", a.State, a.Spec)
			}
		}
	}
	tw.Flush()

	if c.Uncovered > 0 {
		b.WriteString("\nNot covered\n")
		tw = tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
		for _, f := range r.Files {
			if f.Uncovered > 0 {
				fmt.Fprintf(tw, "  %s\t%d of %d\n", f.Path, f.Uncovered, f.Total)
			}
		}
		tw.Flush()
	}

	b.WriteString("\n")
	for _, f := range r.Warnings {
		fmt.Fprintf(&b, "warning: %s\n", f)
	}
	if len(r.Warnings) > 0 {
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "0 errors, %s\n", count(len(r.Warnings), "warning"))
	_, err := io.WriteString(w, b.String())
	return err
}

func sectionSummary(s SectionReport) string {
	if len(s.Anchors) == 0 {
		return "no anchors"
	}
	out := count(s.Shown, "line")
	if s.Claimed != s.Shown {
		out += fmt.Sprintf(" (claims %d, the rest are shown under a more specific section)", s.Claimed)
	}
	return out
}

func count(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func percent(n, total int) string {
	if total == 0 {
		return "0%"
	}
	return fmt.Sprintf("%d%%", (n*100+total/2)/total)
}
