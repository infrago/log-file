package log_file

import (
	"bufio"
	"compress/gzip"
	"container/heap"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type rotatingWriter struct {
	mutex sync.Mutex

	file     *os.File
	buffer   *bufio.Writer
	filename string

	size         int64
	lines        int64
	maxSize      int64
	maxLine      int64
	slice        string
	compress     bool
	maxFiles     int
	maxAge       time.Duration
	closeTimeout time.Duration
	flushEvery   time.Duration
	cleanupEvery time.Duration

	startTime   time.Time
	lastFlush   time.Time
	lastCleanup time.Time
	dirty       bool
	closed      bool

	compressMu     sync.Mutex
	compressActive int
	compressDone   chan struct{}
}

func newRotatingWriter(filename string, maxSize int64, slice string, maxLine int64, compress bool, maxFiles int, maxAge, closeTimeout, flushEvery, cleanupEvery time.Duration) (*rotatingWriter, error) {
	w := &rotatingWriter{
		filename:     filename,
		maxSize:      maxSize,
		maxLine:      maxLine,
		slice:        slice,
		compress:     compress,
		maxFiles:     maxFiles,
		maxAge:       maxAge,
		closeTimeout: closeTimeout,
		flushEvery:   flushEvery,
		cleanupEvery: cleanupEvery,
	}
	w.compressDone = make(chan struct{})
	close(w.compressDone)
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *rotatingWriter) open() error {
	if err := os.MkdirAll(filepath.Dir(w.filename), 0o755); err != nil {
		return err
	}

	file, err := os.OpenFile(w.filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}

	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}

	w.file = file
	w.buffer = bufio.NewWriterSize(file, 64*1024)
	w.size = info.Size()
	w.startTime = info.ModTime()
	w.lastFlush = time.Now()
	return nil
}

func (w *rotatingWriter) Close() error {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	var err error
	w.closed = true
	if e := w.flushBuffer(); e != nil {
		err = e
	}
	if w.file != nil {
		if e := w.file.Close(); e != nil && err == nil {
			err = e
		}
		w.file = nil
	}
	waitErr := w.waitCompression()
	if err == nil {
		err = waitErr
	}
	if e := w.cleanup(true); e != nil && err == nil {
		err = e
	}
	return err
}

func (w *rotatingWriter) waitCompression() error {
	w.compressMu.Lock()
	if w.compressActive == 0 {
		w.compressMu.Unlock()
		return nil
	}
	done := w.compressDone
	w.compressMu.Unlock()

	if w.closeTimeout <= 0 {
		<-done
		return nil
	}
	select {
	case <-done:
		return nil
	case <-time.After(w.closeTimeout):
		return fmt.Errorf("log-file compression close timeout after %s", w.closeTimeout)
	}
}

func (w *rotatingWriter) WriteLine(line string) error {
	return w.WriteLines([]string{line})
}

func (w *rotatingWriter) WriteLines(lines []string) error {
	if len(lines) == 0 {
		return nil
	}
	w.mutex.Lock()
	defer w.mutex.Unlock()

	for _, line := range lines {
		lineBytes := int64(len(line) + 1)

		if w.shouldRotate(lineBytes) {
			if err := w.rotate(); err != nil {
				return err
			}
		}

		if _, err := w.buffer.WriteString(line); err != nil {
			return err
		}
		if err := w.buffer.WriteByte('\n'); err != nil {
			return err
		}

		w.size += lineBytes
		w.lines++
	}

	return w.flushIfDue()
}

func (w *rotatingWriter) flushIfDue() error {
	if w.flushEvery <= 0 {
		return w.flushBuffer()
	}
	w.dirty = true
	return nil
}

func (w *rotatingWriter) Flush() error {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.flushBuffer()
}

func (w *rotatingWriter) flushBuffer() error {
	if w.buffer == nil {
		return nil
	}
	if !w.dirty && w.flushEvery > 0 {
		return nil
	}
	if err := w.buffer.Flush(); err != nil {
		return err
	}
	w.dirty = false
	w.lastFlush = time.Now()
	return nil
}

func (w *rotatingWriter) shouldRotate(incoming int64) bool {
	if w.file == nil {
		return true
	}
	if w.maxSize > 0 && (w.size+incoming) > w.maxSize {
		return true
	}
	if w.maxLine > 0 && w.lines >= w.maxLine {
		return true
	}
	if w.slice != "" && !sameSliceWindow(w.slice, w.startTime, time.Now()) {
		return true
	}
	return false
}

func (w *rotatingWriter) rotate() error {
	if err := w.flushBuffer(); err != nil {
		return err
	}
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			return err
		}
	}

	if _, err := os.Stat(w.filename); err == nil {
		rotated, err := nextRotatedName(w.filename, time.Now())
		if err != nil {
			return err
		}
		if err := os.Rename(w.filename, rotated); err != nil {
			return err
		}
		if w.compress {
			w.beginCompression()
			go func() {
				defer w.finishCompression()
				compressRotatedFile(rotated)
			}()
		}
	}
	if err := w.cleanup(false); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "log-file cleanup failed: %v\n", err)
	}

	w.file = nil
	w.buffer = nil
	w.size = 0
	w.lines = 0
	w.startTime = time.Now()
	return w.open()
}

func (w *rotatingWriter) beginCompression() {
	w.compressMu.Lock()
	if w.compressActive == 0 {
		w.compressDone = make(chan struct{})
	}
	w.compressActive++
	w.compressMu.Unlock()
}

func (w *rotatingWriter) finishCompression() {
	w.compressMu.Lock()
	w.compressActive--
	if w.compressActive == 0 {
		close(w.compressDone)
	}
	w.compressMu.Unlock()
}

func sameSliceWindow(slice string, a, b time.Time) bool {
	switch slice {
	case "year":
		return a.Year() == b.Year()
	case "month":
		return a.Year() == b.Year() && a.Month() == b.Month()
	case "day":
		ay, am, ad := a.Date()
		by, bm, bd := b.Date()
		return ay == by && am == bm && ad == bd
	case "hour":
		ay, am, ad := a.Date()
		by, bm, bd := b.Date()
		return ay == by && am == bm && ad == bd && a.Hour() == b.Hour()
	default:
		return true
	}
}

type rotatedFile struct {
	path string
	at   time.Time
}

type newestRotatedFiles []rotatedFile

func (h newestRotatedFiles) Len() int { return len(h) }
func (h newestRotatedFiles) Less(i, j int) bool {
	return h[i].at.Before(h[j].at)
}
func (h newestRotatedFiles) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *newestRotatedFiles) Push(x any) {
	*h = append(*h, x.(rotatedFile))
}
func (h *newestRotatedFiles) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

func (w *rotatingWriter) cleanup(force bool) error {
	if w.maxFiles <= 0 && w.maxAge <= 0 {
		return nil
	}
	now := time.Now()
	if !force && w.cleanupEvery > 0 && !w.lastCleanup.IsZero() && now.Sub(w.lastCleanup) < w.cleanupEvery {
		return nil
	}
	w.lastCleanup = now

	dir := filepath.Dir(w.filename)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	ext := filepath.Ext(w.filename)
	base := strings.TrimSuffix(filepath.Base(w.filename), ext)
	var newest newestRotatedFiles
	if w.maxFiles > 0 {
		newest = make(newestRotatedFiles, 0, w.maxFiles)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		ts, ok := parseRotatedTimestamp(name, base, ext)
		if !ok {
			continue
		}
		full := filepath.Join(dir, name)
		if w.maxAge > 0 && now.Sub(ts) > w.maxAge {
			_ = os.Remove(full)
			continue
		}
		if w.maxFiles <= 0 {
			continue
		}
		current := rotatedFile{path: full, at: ts}
		if newest.Len() < w.maxFiles {
			heap.Push(&newest, current)
			continue
		}
		if newest[0].at.Before(current.at) {
			oldest := heap.Pop(&newest).(rotatedFile)
			_ = os.Remove(oldest.path)
			heap.Push(&newest, current)
		} else {
			_ = os.Remove(current.path)
		}
	}
	return nil
}

func parseRotatedTimestamp(name, base, ext string) (time.Time, bool) {
	prefix := base + "."
	if !strings.HasPrefix(name, prefix) {
		return time.Time{}, false
	}

	var ts string
	if strings.HasSuffix(name, ext+".gz") {
		ts = strings.TrimSuffix(strings.TrimPrefix(name, prefix), ext+".gz")
	} else if strings.HasSuffix(name, ext) {
		ts = strings.TrimSuffix(strings.TrimPrefix(name, prefix), ext)
	} else {
		return time.Time{}, false
	}

	for _, layout := range []string{"20060102.150405.000000000", "20060102.150405"} {
		t, err := time.ParseInLocation(layout, ts, time.Local)
		if err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func compressRotatedFile(path string) {
	src, err := os.Open(path)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "log-file compress open failed: %v\n", err)
		return
	}
	defer src.Close()

	dstPath := path + ".gz"
	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "log-file compress create failed: %v\n", err)
		return
	}

	gw := gzip.NewWriter(dst)
	_, copyErr := io.Copy(gw, src)
	closeGzipErr := gw.Close()
	closeDstErr := dst.Close()

	if copyErr != nil || closeGzipErr != nil || closeDstErr != nil {
		_ = os.Remove(dstPath)
		if copyErr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "log-file compress copy failed: %v\n", copyErr)
		}
		if closeGzipErr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "log-file compress finalize failed: %v\n", closeGzipErr)
		}
		if closeDstErr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "log-file compress close failed: %v\n", closeDstErr)
		}
		return
	}

	if err := os.Remove(path); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "log-file compress cleanup failed: %v\n", err)
	}
}
