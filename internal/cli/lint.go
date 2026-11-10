package cli

import (
	"context"
	"encoding/json"
	"io"

	"storied/internal/lint"
)

func runLint(args []string, stdout, stderr io.Writer) error {
	fs, bf := newFlags("lint", "[flags]", stderr)
	asJSON := fs.Bool("json", false, "write the report as JSON")
	strict := fs.Bool("strict", false, "exit 1 on warnings as well as errors")
	if err := parse(fs, args); err != nil {
		return err
	}

	res, err := lint.Run(context.Background(), bf.options())
	if err != nil {
		return err
	}
	rep := res.Report
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		err = enc.Encode(rep)
	} else {
		err = rep.WriteText(stdout)
	}
	if err != nil {
		return err
	}
	if len(rep.Errors) > 0 || (*strict && len(rep.Warnings) > 0) {
		return errFailed
	}
	return nil
}
