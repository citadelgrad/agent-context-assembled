package main

import (
	"errors"
	"io"
	"path/filepath"
	"testing"
)

var errOutputSink = errors.New("output sink failed")

type failingOutput struct{ remaining int }

func (w *failingOutput) Write(p []byte) (int, error) {
	if len(p) > w.remaining {
		n := w.remaining
		w.remaining = 0
		return n, errOutputSink
	}
	w.remaining -= len(p)
	return len(p), nil
}

func TestOutputWriterFailuresPropagate(t *testing.T) {
	for _, tc := range []struct {
		name string
		json bool
		cap  int
	}{
		{"text-overflow", false, 1},
		{"json-overflow-control", true, 1},
		{"below-cap-control", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, budget := range []int{0, 8} {
				err := writeSizeGuarded(&failingOutput{remaining: budget}, tc.cap, tc.json,
					filepath.Join(t.TempDir(), "overflow"), func(buf io.Writer) error {
						io.WriteString(buf, "rendered output")
						return nil
					})
				if !errors.Is(err, errOutputSink) {
					t.Errorf("budget %d: got %v, want output sink error", budget, err)
				}
			}
		})
	}
	for _, args := range [][]string{{"--version"}, {"--tool=list"}, {"--tool=list", "--json"}} {
		for _, budget := range []int{0, 5} {
			if err := run(args, &failingOutput{remaining: budget}); !errors.Is(err, errOutputSink) {
				t.Errorf("args %v budget %d: got %v, want output sink error", args, budget, err)
			}
		}
	}
}
