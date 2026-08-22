package tail

import (
	"bytes"
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	scanInterval = 60 * time.Second
	readInterval = 1 * time.Second
	staleAfter   = 15 * time.Minute
	logPattern   = "output_log_*.txt"
	tsLayout     = "2006.01.02 15:04:05"
)

type Callbacks struct {
	// ファイルを(再)オープンした。パーサーを完全リセットすること
	OnFileSwitch func(path string)
	OnLine       func(ts time.Time, msg string)
	// 最初のEOFに到達しLIVEへ遷移した
	OnCaughtUp func()
	OnTick     func(now time.Time)
	// 書き込みがstaleAfterの間停止した。VRChat終了とみなす
	OnStale func()
}

type tailer struct {
	dir string
	cb  Callbacks

	file       *os.File
	path       string
	live       bool
	stale      bool
	staleMtime time.Time

	buf      []byte
	lastTS   time.Time
	chunkBuf []byte
}

func Run(ctx context.Context, dir string, cb Callbacks) error {
	t := &tailer{dir: dir, cb: cb, chunkBuf: make([]byte, 64*1024)}
	scanT := time.NewTicker(scanInterval)
	defer scanT.Stop()
	readT := time.NewTicker(readInterval)
	defer readT.Stop()

	t.scan()

	for {
		select {
		case <-ctx.Done():
			if t.file != nil {
				t.file.Close()
			}
			return ctx.Err()
		case <-scanT.C:
			t.scan()
		case <-readT.C:
			t.poll()
		}
	}
}

func (t *tailer) scan() {
	latest, mtime, ok := latestLog(t.dir)
	if !ok {
		return
	}
	if latest != t.path {
		t.attach(latest)
		return
	}
	if t.stale {
		if mtime.After(t.staleMtime) {
			t.attach(latest)
		}
		return
	}
	if t.file != nil && time.Since(mtime) > staleAfter {
		t.stale = true
		t.staleMtime = mtime
		t.live = false
		t.file.Close()
		t.file = nil
		log.Printf("tail: %s idle for %v, assuming VRChat exited", filepath.Base(t.path), staleAfter)
		if t.cb.OnStale != nil {
			t.cb.OnStale()
		}
	}
}

func (t *tailer) attach(path string) {
	if t.file != nil {
		t.file.Close()
		t.file = nil
	}
	f, err := os.Open(path)
	if err != nil {
		log.Printf("tail: open failed: %v", err)
		return
	}
	t.file = f
	t.path = path
	t.live = false
	t.stale = false
	t.buf = t.buf[:0]
	t.lastTS = time.Time{}
	log.Printf("tail: replaying %s", filepath.Base(path))
	if t.cb.OnFileSwitch != nil {
		t.cb.OnFileSwitch(path)
	}
	t.readAvailable()
	t.live = true
	if t.cb.OnCaughtUp != nil {
		t.cb.OnCaughtUp()
	}
}

func (t *tailer) poll() {
	if t.file != nil {
		t.readAvailable()
	}
	if t.live && t.cb.OnTick != nil {
		t.cb.OnTick(time.Now())
	}
}

func (t *tailer) readAvailable() {
	for {
		n, err := t.file.Read(t.chunkBuf)
		if n > 0 {
			t.buf = append(t.buf, t.chunkBuf[:n]...)
			t.drainLines()
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("tail: read failed: %v", err)
			}
			return
		}
	}
}

func (t *tailer) drainLines() {
	start := 0
	for {
		i := bytes.IndexByte(t.buf[start:], '\n')
		if i < 0 {
			break
		}
		t.handleLine(t.buf[start : start+i])
		start += i + 1
	}
	if start > 0 {
		t.buf = append(t.buf[:0], t.buf[start:]...)
	}
}

func (t *tailer) handleLine(b []byte) {
	s := strings.TrimRight(string(b), "\r")
	if s == "" {
		return
	}
	s = strings.ToValidUTF8(s, "�")
	if ts, msg, ok := parseHeader(s); ok {
		t.lastTS = ts
		if t.cb.OnLine != nil {
			t.cb.OnLine(ts, msg)
		}
		return
	}
	// ヘッダのない継続行は直前行のtimestampを継承する
	if !t.lastTS.IsZero() && t.cb.OnLine != nil {
		t.cb.OnLine(t.lastTS, s)
	}
}

// "YYYY.MM.DD HH:MM:SS <レベル> -  " のヘッダを剥がす
func parseHeader(s string) (time.Time, string, bool) {
	if len(s) < len(tsLayout) {
		return time.Time{}, "", false
	}
	ts, err := time.ParseInLocation(tsLayout, s[:len(tsLayout)], time.Local)
	if err != nil {
		return time.Time{}, "", false
	}
	rest := s[len(tsLayout):]
	if _, after, ok := strings.Cut(rest, "-  "); ok {
		return ts, after, true
	}
	return ts, strings.TrimSpace(rest), true
}

func latestLog(dir string) (string, time.Time, bool) {
	matches, err := filepath.Glob(filepath.Join(dir, logPattern))
	if err != nil || len(matches) == 0 {
		return "", time.Time{}, false
	}
	// ファイル名の日時は0埋めなので辞書順最大が最新
	sort.Strings(matches)
	p := matches[len(matches)-1]
	fi, err := os.Stat(p)
	if err != nil {
		return "", time.Time{}, false
	}
	return p, fi.ModTime(), true
}
