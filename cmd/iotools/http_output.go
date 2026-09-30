package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Live-yum/iotools/internal/engine"
	"io"
	"os"
)

type cliExitError struct {
	code int
	err  error
}

func (e cliExitError) Error() string { return e.err.Error() }
func (e cliExitError) Unwrap() error { return e.err }

type httpDisplay struct {
	raw, derived         []byte
	status               int
	transformed, verbose bool
	output               string
	stderr               io.Writer
	err                  error
}

func (d *httpDisplay) event(e engine.Event) {
	data, ok := e.Data.(map[string]any)
	if !ok {
		return
	}
	if e.Kind == "response" || e.Kind == "response-file" {
		d.status, _ = data["status"].(int)
		if d.verbose {
			fmt.Fprintf(d.stderr, "HTTP %d\n", d.status)
			b, _ := json.MarshalIndent(data["headers"], "", "  ")
			fmt.Fprintln(d.stderr, string(b))
		}
		if raw, ok := data["raw_body_base64"].(string); ok {
			d.raw, d.err = base64.StdEncoding.DecodeString(raw)
		}
	}
	if e.Kind == "transformed" {
		d.derived, d.err = json.MarshalIndent(data["body"], "", "  ")
	}
}
func (d *httpDisplay) finish(stdout io.Writer, runErr error, exitStatus bool) error {
	if d.err != nil {
		return d.err
	}
	body := d.raw
	if d.transformed {
		if d.derived != nil {
			body = d.derived
		} else {
			var transformError *engine.HTTPResponseTransformError
			if errors.As(runErr, &transformError) {
				return cliExitError{3, runErr}
			}
		}
	}
	if d.output != "" && d.transformed {
		if runErr != nil && d.raw == nil && d.derived == nil {
			return runErr
		}
		f, err := os.OpenFile(d.output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, err = f.Write(body)
		closeErr := f.Close()
		if err != nil {
			os.Remove(d.output)
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	} else if d.output == "" {
		if _, err := stdout.Write(body); err != nil {
			return err
		}
	}
	if exitStatus && d.status >= 400 {
		return cliExitError{2, fmt.Errorf("HTTP %d", d.status)}
	}
	return runErr
}
