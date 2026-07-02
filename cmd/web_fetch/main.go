package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	webtools "github.com/openmodu/modu/pkg/coding_agent/tools/web"
	"github.com/spf13/cobra"
)

type fetchCLIOptions struct {
	maxBytes int
	timeout  time.Duration
	jsWait   time.Duration
	raw      bool
	jsRender bool
	json     bool
	output   string
}

func main() {
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	opts := fetchCLIOptions{}
	cmd := &cobra.Command{
		Use:   "web_fetch [url]",
		Short: "Fetch a web page and print readable Markdown",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFetch(cmd.Context(), args[0], opts, os.Stdout)
		},
	}
	cmd.Flags().IntVar(&opts.maxBytes, "max-bytes", 2*1024*1024, "maximum response bytes to read")
	cmd.Flags().DurationVar(&opts.timeout, "timeout", 15*time.Second, "HTTP request timeout")
	cmd.Flags().BoolVar(&opts.jsRender, "js", false, "render JavaScript in a headless browser before extraction")
	cmd.Flags().DurationVar(&opts.jsWait, "js-wait", 2*time.Second, "extra wait after page load when --js is enabled")
	cmd.Flags().BoolVar(&opts.raw, "raw", false, "print raw response body without HTML extraction")
	cmd.Flags().BoolVar(&opts.json, "json", false, "print JSON with metadata and content")
	cmd.Flags().StringVarP(&opts.output, "output", "o", "", "write output to file instead of stdout")
	return cmd
}

func runFetch(ctx context.Context, target string, opts fetchCLIOptions, out io.Writer) error {
	client := &http.Client{Timeout: opts.timeout}
	page, err := webtools.Fetch(ctx, client, target, webtools.FetchOptions{
		MaxBytes: opts.maxBytes,
		Raw:      opts.raw,
		JSRender: opts.jsRender,
		JSWait:   opts.jsWait,
	})
	if err != nil {
		return err
	}

	var data []byte
	if opts.json {
		data, err = page.JSON()
		if err != nil {
			return err
		}
		data = append(data, '\n')
	} else {
		data = []byte(page.Content)
		if len(data) == 0 || data[len(data)-1] != '\n' {
			data = append(data, '\n')
		}
	}

	if opts.output == "" {
		_, err = out.Write(data)
		return err
	}
	return os.WriteFile(opts.output, data, 0o644)
}
