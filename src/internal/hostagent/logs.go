package hostagent

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"time"
)

// logs.tail / logs.stop (FLEET.md §18.2)
//
// The stream id is the logs.tail message's own id, so the platform knows it
// without another field. The host.result (ok) goes out first, then
// logs.chunk frames (ephemeral) with the last `lines` lines; with follow the
// file is watched for new lines until max_s (default 300 s), logs.stop or the
// connection ends. The last chunk has eof: true. Every line is redacted.

const (
	maxFollowStreams = 4
	maxLineBytes     = 2048
	chunkLines       = 200
	chunkBytes       = 256 << 10
	tailReadBytes    = 4 << 20
)

func (a *Agent) handleLogsTail(ref string, c *LogsTailCmd) {
	n := 0
	if c.Server != "" {
		n, _ = ParseServerName(c.Server)
	}
	path, err := a.opts.Backend.LogFile(n, c.Source)
	if err != nil {
		a.sendResult(ref, rejected(CodeNotFound, err.Error()))
		return
	}
	f, err := os.Open(path)
	if err != nil {
		a.sendResult(ref, rejected(CodeNotFound, "the "+c.Source+" log is not there yet"))
		return
	}
	lines := 200
	if c.Lines != nil {
		lines = *c.Lines
	}
	maxS := 300
	if c.MaxS != nil {
		maxS = *c.MaxS
	}

	var ctx context.Context
	var cancel context.CancelFunc
	if c.Follow {
		a.logsMu.Lock()
		if len(a.logs) >= maxFollowStreams {
			a.logsMu.Unlock()
			f.Close()
			a.sendResult(ref, rejected(CodeLimit, "at most 4 followed logs at a time; stop one with logs.stop"))
			return
		}
		ctx, cancel = context.WithTimeout(context.Background(), time.Duration(maxS)*time.Second)
		a.logs[ref] = cancel
		a.logsMu.Unlock()
	}
	a.sendResult(ref, okResult("stream "+ref))

	offset, initial := readTail(f, lines)
	a.sendChunks(ref, c.Server, initial, !c.Follow)
	if !c.Follow {
		f.Close()
		return
	}
	go func() {
		defer f.Close()
		defer func() {
			a.logsMu.Lock()
			delete(a.logs, ref)
			a.logsMu.Unlock()
			cancel()
		}()
		a.follow(ctx, f, offset, ref, c.Server)
		a.sendChunks(ref, c.Server, nil, true)
	}()
}

func (a *Agent) handleLogsStop(c *LogsStopCmd) ResultPayload {
	a.logsMu.Lock()
	cancel, ok := a.logs[c.StreamID]
	a.logsMu.Unlock()
	if !ok {
		return rejected(CodeNotFound, "no such log stream (it may have ended)")
	}
	cancel()
	return okResult("stopped")
}

// stopSessionLogs ends every followed log when the connection drops: the
// chunks are ephemeral and could not reach the platform anyway.
func (a *Agent) stopSessionLogs() { a.stopAllLogs() }

func (a *Agent) stopAllLogs() {
	a.logsMu.Lock()
	for _, cancel := range a.logs {
		cancel()
	}
	a.logsMu.Unlock()
}

// readTail returns the last n lines of f and the offset after them.
func readTail(f *os.File, n int) (int64, []string) {
	fi, err := f.Stat()
	if err != nil {
		return 0, nil
	}
	size := fi.Size()
	start := size - tailReadBytes
	if start < 0 {
		start = 0
	}
	buf := make([]byte, size-start)
	if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
		return size, nil
	}
	if start > 0 {
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		}
	}
	// A trailing partial line is left for follow.
	end := bytes.LastIndexByte(buf, '\n')
	complete := buf
	if end >= 0 {
		complete = buf[:end]
	} else {
		complete = nil
	}
	consumed := size - int64(len(buf)-(end+1))
	if n == 0 || len(complete) == 0 {
		return consumed, nil
	}
	all := strings.Split(string(complete), "\n")
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return consumed, all
}

func (a *Agent) follow(ctx context.Context, f *os.File, offset int64, ref, server string) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	var partial []byte
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		fi, err := f.Stat()
		if err != nil {
			return
		}
		if fi.Size() < offset {
			// Truncated or rotated in place.
			offset = 0
			partial = nil
		}
		if fi.Size() == offset {
			continue
		}
		r := bufio.NewReader(io.NewSectionReader(f, offset, fi.Size()-offset))
		data, _ := io.ReadAll(io.LimitReader(r, tailReadBytes))
		offset += int64(len(data))
		data = append(partial, data...)
		end := bytes.LastIndexByte(data, '\n')
		if end < 0 {
			partial = data
			continue
		}
		partial = append([]byte(nil), data[end+1:]...)
		a.sendChunks(ref, server, strings.Split(string(data[:end]), "\n"), false)
	}
}

// sendChunks splits lines into logs.chunk frames; eof marks the last.
func (a *Agent) sendChunks(ref, server string, lines []string, eof bool) {
	var batch []string
	size := 0
	flush := func(last bool) {
		if len(batch) == 0 && !last {
			return
		}
		p := LogsChunkPayload{StreamID: ref, Server: server, Lines: batch, EOF: last}
		if p.Lines == nil {
			p.Lines = []string{}
		}
		a.sendEphemeral(TypeLogsChunk, p, "")
		batch = nil
		size = 0
	}
	for _, l := range lines {
		l = truncate(Redact(strings.TrimRight(l, "\r")), maxLineBytes)
		if len(batch) >= chunkLines || size+len(l) > chunkBytes {
			flush(false)
		}
		batch = append(batch, l)
		size += len(l) + 8
	}
	flush(eof)
}
