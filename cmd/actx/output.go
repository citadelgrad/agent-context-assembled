package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/citadelgrad/agent-context-assembled/internal/render"
)

// writeSizeGuarded stages capped output in bounded memory with lazy disk spill.
// Unlimited output streams directly; callback errors can then leave partial stdout.
// maxChars is a delivery cap in codepoints, not the staging memory budget.
func writeSizeGuarded(w io.Writer, maxChars int, jsonMode bool, outputPath string, renderOutput func(buf io.Writer) error) error {
	if maxChars <= 0 {
		return renderTo(w, renderOutput)
	}
	stage := &outputStage{}
	defer stage.cleanup()
	if err := renderTo(stage, renderOutput); err != nil {
		return err
	}
	source, err := stage.reader()
	if err != nil {
		return err
	}
	// Reading runes from the byte stream handles split writes and malformed UTF-8
	// exactly like utf8.RuneCount, including an incomplete sequence at EOF.
	counter := bufio.NewReader(source)
	charCount := 0
	for {
		_, _, err := counter.ReadRune()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		charCount++
	}
	source, err = stage.reader()
	if err != nil {
		return err
	}
	if charCount <= maxChars {
		_, err := io.Copy(w, source)
		return err
	}

	ext := ".txt"
	if jsonMode {
		ext = ".json"
	}
	actualPath, err := prepareOverflowFile(outputPath, ext, source)
	if err != nil {
		return fmt.Errorf("output exceeded %d chars but could not write overflow file: %w", maxChars, err)
	}

	published := false
	defer func() {
		if !published {
			_ = os.Remove(actualPath)
		}
	}()
	noticePath := actualPath
	if outputPath != "" {
		if err := os.Rename(actualPath, outputPath); err != nil {
			return fmt.Errorf("replacing %s: %w", outputPath, err)
		}
		published = true
		noticePath = outputPath
	}
	// Publish before exposing the path: a pipe consumer may open it during this
	// write. A notice failure cannot roll back a caller-chosen file that may
	// already have been read; keep the complete published file and report the error.
	if err := renderTo(w, func(notice io.Writer) error {
		return writeOverflowNotice(notice, maxChars, charCount, jsonMode, noticePath)
	}); err != nil {
		return err
	}
	published = true
	return nil
}

func writeOverflowNotice(w io.Writer, maxChars, charCount int, jsonMode bool, actualPath string) error {
	tokenEstimate := charCount / 4
	if jsonMode {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(outputOverflow{
			Truncated:     true,
			Reason:        fmt.Sprintf("output exceeded the %d-char safety cap (~%d tokens); re-run with --max-chars=0 to print it directly, or a higher --max-chars value", maxChars, maxChars/4),
			CharCount:     charCount,
			TokenEstimate: tokenEstimate,
			OutputFile:    actualPath,
		})
	}
	_, err := fmt.Fprintf(w, "Output too large: %d chars (~%d tokens) exceeds the %d-char safety cap.\n"+
		"Full output written to: %s\n"+
		"Re-run with --max-chars=0 to print it directly instead, or a higher --max-chars value.\n",
		charCount, tokenEstimate, maxChars, render.SafeText(actualPath))
	return err
}

// prepareOverflowFile returns a complete, closed file, not yet committed to --out.
// The caller must publish it before issuing a notice, or remove it on failure.
func prepareOverflowFile(outputPath, ext string, content io.Reader) (string, error) {
	if outputPath == "" {
		f, err := os.CreateTemp("", "actx-output-*"+ext)
		if err != nil {
			return "", err
		}
		path := f.Name()
		if _, err := io.Copy(f, content); err != nil {
			_ = f.Close()
			_ = os.Remove(path)
			return "", fmt.Errorf("writing %s: %w", path, err)
		}
		if err := f.Close(); err != nil {
			_ = os.Remove(path)
			return "", fmt.Errorf("closing %s: %w", path, err)
		}
		return path, nil
	}

	mode := os.FileMode(0o600)
	if info, err := os.Stat(outputPath); err == nil {
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return "", err
	}
	dir := filepath.Dir(outputPath)
	tmp, err := os.CreateTemp(dir, ".actx-output-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if err := tmp.Chmod(mode); err != nil {
		cleanup()
		return "", err
	}
	if _, err := io.Copy(tmp, content); err != nil {
		cleanup()
		return "", fmt.Errorf("writing %s: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return "", fmt.Errorf("syncing %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("closing %s: %w", tmpPath, err)
	}
	return tmpPath, nil
}

// outputMemoryBytes bounds the guard's retained in-memory rendered bytes.
// JSON encoders, compilation and text formatting may still allocate input-derived objects.
const outputMemoryBytes = 256 * 1024

// Capped output may stage at most 64 MiB, independently of --max-chars.
// Atomic publication can temporarily require a second copy (128 MiB total).
// --max-chars=0 streams without this disk budget.
const outputStageBytes = 64 * 1024 * 1024

type outputStage struct {
	size   int
	memory []byte
	spool  *os.File
}

func (s *outputStage) Write(p []byte) (int, error) {
	if len(p) > outputStageBytes-s.size {
		return 0, fmt.Errorf("exceeded %d-byte output staging limit (separate from --max-chars); re-run with --max-chars=0 to stream directly", outputStageBytes)
	}
	s.size += len(p)
	if s.spool == nil && len(p) <= outputMemoryBytes-len(s.memory) {
		if len(p) > 0 && s.memory == nil {
			s.memory = make([]byte, 0, outputMemoryBytes)
		}
		s.memory = append(s.memory, p...)
		return len(p), nil
	}
	if s.spool == nil {
		f, err := os.CreateTemp("", "actx-stage-*")
		if err != nil {
			return 0, fmt.Errorf("creating output staging file: %w", err)
		}
		s.spool = f
		if _, err := f.Write(s.memory); err != nil {
			return 0, fmt.Errorf("staging output: %w", err)
		}
		s.memory = nil
	}
	n, err := s.spool.Write(p)
	if err != nil {
		return n, fmt.Errorf("staging output: %w", err)
	}
	return n, nil
}

func (s *outputStage) reader() (io.Reader, error) {
	if s.spool == nil {
		return bytes.NewReader(s.memory), nil
	}
	if _, err := s.spool.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return s.spool, nil
}

func (s *outputStage) cleanup() {
	if s.spool != nil {
		_ = s.spool.Close()
		_ = os.Remove(s.spool.Name())
	}
}

// renderTo confines a private abort to a synchronous renderer invocation. Text
// renderers ignore fmt write errors; returning an error alone would allow them to
// keep formatting the remaining input after disk/sink failure. Only this exact
// writer's abort is recovered; unrelated callback panics propagate unchanged.
func renderTo(w io.Writer, renderOutput func(io.Writer) error) (err error) {
	sink := &stickyOutput{w: w}
	defer func() {
		if p := recover(); p != nil {
			if failed, ok := p.(*stickyOutput); !ok || failed != sink {
				panic(p)
			}
			err = sink.err
		}
	}()
	err = renderOutput(sink)
	if sink.err != nil {
		return sink.err
	}
	return err
}

// stickyOutput preserves errors even if a callback recovers its private abort.
type stickyOutput struct {
	w   io.Writer
	err error
}

func (w *stickyOutput) Write(p []byte) (int, error) {
	if w.err != nil {
		panic(w)
	}
	n, err := w.w.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	w.err = err
	if err != nil {
		panic(w)
	}
	return n, nil
}
