package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/nickelsey/storied/internal/lint"
	"github.com/nickelsey/storied/internal/render"
)

func runRender(args []string, stdout, stderr io.Writer) error {
	fs, bf := newFlags("render", "[flags]", stderr)
	out := fs.String("o", "", "output `file`, or - for standard output (default: story.html in the bundle directory)")
	if err := parse(fs, args); err != nil {
		return err
	}

	res, err := lint.Run(context.Background(), bf.options())
	if err != nil {
		return err
	}
	rep := res.Report
	if len(rep.Errors) > 0 {
		if err := rep.WriteText(stderr); err != nil {
			return err
		}
		return errFailed
	}

	var page bytes.Buffer
	if err := render.Page(&page, res); err != nil {
		return err
	}
	if *out == "-" {
		_, err := stdout.Write(page.Bytes())
		return err
	}
	path := *out
	if path == "" {
		path = filepath.Join(res.Bundle.Dir, "story.html")
	}
	if err := os.WriteFile(path, page.Bytes(), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "wrote %s\n", path)
	if n := len(rep.Warnings); n > 0 {
		fmt.Fprintf(stderr, "%d lint warnings; run storied lint to see them\n", n)
	}
	return nil
}
