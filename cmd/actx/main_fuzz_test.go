package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/citadelgrad/agent-context-assembled/internal/inspect"
	"github.com/citadelgrad/agent-context-assembled/internal/scan"
	"github.com/citadelgrad/agent-context-assembled/internal/tools"
)

func FuzzWantsJSONLastRecognizedOccurrenceWins(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3})
	f.Add([]byte{2, 0, 5, 1})
	f.Fuzz(func(t *testing.T, operations []byte) {
		if len(operations) > 128 {
			operations = operations[:128]
		}
		var args []string
		want := false
		for _, operation := range operations {
			switch operation % 6 {
			case 0:
				args = append(args, "--json")
				want = true
			case 1:
				args = append(args, "--json=false")
				want = false
			case 2:
				args = append(args, "-json=true")
				want = true
			case 3:
				args = append(args, "--json=invalid")
			case 4:
				args = append(args, "--other")
			case 5:
				args = append(args, "path")
			}
		}
		if got := wantsJSON(args); got != want {
			t.Fatalf("wantsJSON(%v)=%v, want %v", args, got, want)
		}
	})
}

func FuzzFilterResultsAndReportsPreserveOrder(f *testing.F) {
	f.Add([]byte{0, 1, 1, 0})
	f.Fuzz(func(t *testing.T, selected []byte) {
		registry := tools.Registry
		want := map[string]bool{}
		for i, b := range selected {
			if i >= len(registry) {
				break
			}
			if b%2 == 1 {
				want[registry[i].Slug] = true
			}
		}
		results := make([]scan.ToolResult, len(registry))
		reports := make([]inspect.Report, len(registry))
		for i, tool := range registry {
			results[i] = scan.ToolResult{Tool: tool}
			reports[i] = inspect.Report{Slug: tool.Slug}
		}
		gotResults, gotReports := filterResults(results, want), filterReports(reports, want)
		if len(gotResults) != len(want) || len(gotReports) != len(want) {
			t.Fatalf("lengths %d/%d want %d", len(gotResults), len(gotReports), len(want))
		}
		j := 0
		for _, tool := range registry {
			if want[tool.Slug] {
				if gotResults[j].Tool.Slug != tool.Slug || gotReports[j].Slug != tool.Slug {
					t.Fatal("filter changed registry order")
				}
				j++
			}
		}
		if twice := filterResults(gotResults, want); !reflect.DeepEqual(twice, gotResults) {
			t.Fatal("filterResults is not idempotent")
		}
		if twice := filterReports(gotReports, want); !reflect.DeepEqual(twice, gotReports) {
			t.Fatal("filterReports is not idempotent")
		}
		if got := filterResults(results, nil); !reflect.DeepEqual(got, results) {
			t.Fatal("nil result filter changed values or order")
		}
		if got := filterReports(reports, nil); !reflect.DeepEqual(got, reports) {
			t.Fatal("nil report filter changed values or order")
		}
	})
}

func TestRunArgumentClassificationProperties(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		args  []string
		usage bool
	}{
		{[]string{"--tool=unknown", dir}, true},
		{[]string{dir, "--json"}, true},
		{[]string{"--max-chars=bad", dir}, true},
		{[]string{"--json", dir}, false},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.args), func(t *testing.T) {
			var out bytes.Buffer
			err := run(tt.args, &out)
			var usage *usageError
			if errors.As(err, &usage) != tt.usage {
				t.Fatalf("error=%v usage=%v, want %v", err, errors.As(err, &usage), tt.usage)
			}
			if errors.Is(err, flag.ErrHelp) {
				t.Fatal("unexpected help")
			}
			if tt.usage && !strings.Contains(err.Error(), "unknown") && !strings.Contains(err.Error(), "unexpected") && !strings.Contains(err.Error(), "invalid") {
				t.Fatalf("unhelpful usage error: %v", err)
			}
		})
	}
}
