package planusage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// FileMark is how far one transcript has been metered.
type FileMark struct {
	// Offset counts bytes of complete lines read.
	Offset int64 `json:"offset"`
	// Last is the last message id counted, so a message whose lines straddle
	// two reads is counted once.
	Last string `json:"last,omitempty"`
}

// Entry is the cost of one assistant message.
type Entry struct {
	At   time.Time
	Cost float64
}

// ReadResult is what one read of a transcript found.
type ReadResult struct {
	Mark    FileMark
	Entries []Entry
	// Unpriced is the model of the message reading stopped at: it has no price.
	Unpriced string
	// Bad is set when reading stopped at a line that could not be parsed.
	Bad bool
}

// Cost sums the entries.
func (r ReadResult) Cost() float64 {
	total := 0.0
	for _, e := range r.Entries {
		total += e.Cost
	}
	return total
}

type usage struct {
	InputTokens              int64 `json:"input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreation            *struct {
		Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
		Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

func (u usage) cost(pr Price) float64 {
	w5, w1 := u.CacheCreationInputTokens, int64(0)
	if c := u.CacheCreation; c != nil && c.Ephemeral5m+c.Ephemeral1h > 0 {
		w5, w1 = c.Ephemeral5m, c.Ephemeral1h
	}
	return (float64(u.InputTokens)*pr.Input + float64(w5)*pr.CacheWrite5m + float64(w1)*pr.CacheWrite1h +
		float64(u.CacheReadInputTokens)*pr.CacheRead + float64(u.OutputTokens)*pr.Output) / 1e6
}

func (u usage) empty() bool {
	return u.InputTokens+u.CacheCreationInputTokens+u.CacheReadInputTokens+u.OutputTokens == 0
}

type transcriptLine struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Message   *struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *usage `json:"usage"`
	} `json:"message"`
}

var usageKey = []byte(`"usage"`)

// ReadTranscript meters a Claude Code transcript from mark: complete lines
// only, each assistant message once by message id. It stops before a line it
// cannot price, or before a line it cannot parse unless skipBad is set, so the
// next read starts there again. A file shorter than the mark was rewritten and
// is read from the start.
func ReadTranscript(path string, mark FileMark, overrides map[string]Price, skipBad bool) (ReadResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return ReadResult{Mark: mark}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return ReadResult{Mark: mark}, err
	}
	if info.Size() < mark.Offset {
		mark = FileMark{}
	}
	res := ReadResult{Mark: mark}
	if info.Size() == mark.Offset {
		return res, nil
	}
	if _, err := f.Seek(mark.Offset, io.SeekStart); err != nil {
		return res, err
	}
	seen := map[string]bool{}
	if mark.Last != "" {
		seen[mark.Last] = true
	}
	r := bufio.NewReaderSize(f, 64*1024)
	for {
		line, err := r.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			return res, nil
		}
		if err != nil {
			return res, err
		}
		next := res.Mark.Offset + int64(len(line))
		if !bytes.Contains(line, usageKey) {
			res.Mark.Offset = next
			continue
		}
		var l transcriptLine
		if err := json.Unmarshal(line, &l); err != nil {
			if !skipBad {
				res.Bad = true
				return res, nil
			}
			res.Mark.Offset = next
			continue
		}
		msg := l.Message
		if l.Type != "assistant" || msg == nil || msg.Usage == nil || msg.Usage.empty() || (msg.ID != "" && seen[msg.ID]) {
			res.Mark.Offset = next
			continue
		}
		pr, ok := PriceFor(msg.Model, overrides)
		if !ok {
			res.Unpriced = msg.Model
			return res, nil
		}
		at := l.Timestamp
		if at.IsZero() {
			at = info.ModTime()
		}
		res.Entries = append(res.Entries, Entry{At: at, Cost: msg.Usage.cost(pr)})
		if msg.ID != "" {
			seen[msg.ID] = true
			res.Mark.Last = msg.ID
		}
		res.Mark.Offset = next
	}
}

// TranscriptFiles lists the transcripts in Claude project directories: each
// conversation file and its subagents' files.
func TranscriptFiles(projectDirs []string) []string {
	var out []string
	for _, dir := range projectDirs {
		for _, pattern := range []string{"*.jsonl", filepath.Join("*", "subagents", "*.jsonl")} {
			matches, _ := filepath.Glob(filepath.Join(dir, pattern))
			for _, m := range matches {
				if info, err := os.Stat(m); err == nil && info.Mode().IsRegular() {
					out = append(out, m)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}
