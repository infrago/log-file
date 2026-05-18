package log_file

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	blog "github.com/infrago/log"
)

func TestRotatedNameIncludesNanoseconds(t *testing.T) {
	got := filepath.Base(rotatedName("app.log", time.Date(2026, 5, 16, 10, 11, 12, 13, time.UTC)))
	want := "app.20260516.101112.000000013.log"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestParseRotatedTimestampSupportsOldAndNewNames(t *testing.T) {
	for _, name := range []string{
		"app.20260516.101112.log",
		"app.20260516.101112.000000013.log",
		"app.20260516.101112.000000013.log.gz",
	} {
		if _, ok := parseRotatedTimestamp(name, "app", ".log"); !ok {
			t.Fatalf("expected %s to parse as rotated log", name)
		}
	}
}

func TestParseRotatedTimestampUsesLocalTimezone(t *testing.T) {
	got, ok := parseRotatedTimestamp("app.20260516.101112.000000013.log", "app", ".log")
	if !ok {
		t.Fatal("expected rotated timestamp to parse")
	}
	want := time.Date(2026, 5, 16, 10, 11, 12, 13, time.Local)
	if !got.Equal(want) || got.Location() != time.Local {
		t.Fatalf("expected %v in local timezone, got %v in %v", want, got, got.Location())
	}
}

func TestNextRotatedNameAvoidsExistingName(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "app.log")
	now := time.Date(2026, 5, 16, 10, 11, 12, 13, time.UTC)
	first := rotatedName(file, now)
	if err := os.WriteFile(first, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	next, err := nextRotatedName(file, now)
	if err != nil {
		t.Fatal(err)
	}
	if next == first {
		t.Fatalf("expected a different rotated name when %s exists", first)
	}
}

func TestOpenUsesExistingFileModTimeForSliceWindow(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "app.log")
	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.WriteFile(file, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	w, err := newRotatingWriter(file, 0, "hour", 0, false, 0, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if sameSliceWindow("hour", w.startTime, time.Now()) {
		t.Fatalf("expected stale file mod time to fall outside current hour")
	}
}

func TestFileConnectionOpenRollsBackOpenedWriters(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "not-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	conn := &fileConnection{
		setting: fileSetting{
			store:      dir,
			output:     "output.log",
			levelFiles: map[blog.Level]string{blog.LevelError: filepath.Join(blocker, "error.log")},
			maxSize:    1024,
		},
		writers: map[blog.Level]*rotatingWriter{},
	}

	if err := conn.Open(); err == nil {
		t.Fatal("expected open to fail")
	}
	if writer := conn.writers[outputBucket]; writer == nil || writer.file != nil {
		t.Fatalf("expected already-opened output writer to be closed on rollback")
	}
}

func TestFileConnectionReusesWriterForSamePath(t *testing.T) {
	dir := t.TempDir()
	inst := &blog.Instance{
		Config: blog.Config{Format: "%body%"},
	}
	conn := &fileConnection{
		instance: inst,
		setting: fileSetting{
			store:      dir,
			output:     "app.log",
			levelFiles: map[blog.Level]string{blog.LevelError: "app.log"},
			maxSize:    1024,
		},
		writers: map[blog.Level]*rotatingWriter{},
	}
	if err := conn.Open(); err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if conn.writers[outputBucket] != conn.writers[blog.LevelError] {
		t.Fatal("expected output and error to share one writer for the same path")
	}
	if err := conn.Write(blog.Log{Time: time.Now(), Level: blog.LevelError, Body: "boom"}); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "app.log"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(body); got != "boom\n" {
		t.Fatalf("expected one write without duplicate level output, got %q", got)
	}
}

func TestFileConnectionReusesWriterForSymlinkedPath(t *testing.T) {
	dir := t.TempDir()
	realDir := filepath.Join(dir, "real")
	linkDir := filepath.Join(dir, "link")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	conn := &fileConnection{
		instance: &blog.Instance{Config: blog.Config{Format: "%body%"}},
		setting: fileSetting{
			output:     filepath.Join(realDir, "app.log"),
			levelFiles: map[blog.Level]string{blog.LevelError: filepath.Join(linkDir, "app.log")},
			maxSize:    1024,
		},
		writers: map[blog.Level]*rotatingWriter{},
	}
	if err := conn.Open(); err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if conn.writers[outputBucket] != conn.writers[blog.LevelError] {
		t.Fatal("expected symlinked paths to share one writer")
	}
}

func TestFileDriverParsesCloseTimeout(t *testing.T) {
	connAny, err := (&fileDriver{}).Connect(&blog.Instance{Setting: map[string]any{
		"close_timeout":    "25ms",
		"flush_interval":   "30ms",
		"cleanup_interval": "35ms",
	}})
	if err != nil {
		t.Fatal(err)
	}
	conn := connAny.(*fileConnection)
	if conn.setting.closeTimeout != 25*time.Millisecond {
		t.Fatalf("expected close timeout to parse, got %s", conn.setting.closeTimeout)
	}
	if conn.setting.flushEvery != 30*time.Millisecond {
		t.Fatalf("expected flush interval to parse, got %s", conn.setting.flushEvery)
	}
	if conn.setting.cleanupEvery != 35*time.Millisecond {
		t.Fatalf("expected cleanup interval to parse, got %s", conn.setting.cleanupEvery)
	}
}

func TestRotatingWriterFlushIntervalDelaysFlushUntilClose(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "app.log")
	w, err := newRotatingWriter(file, 0, "", 0, false, 0, 0, 0, time.Hour, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WriteLines([]string{"buffered"}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "" {
		t.Fatalf("expected delayed flush to keep file empty before Close, got %q", string(body))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "buffered\n" {
		t.Fatalf("expected Close to flush buffered log, got %q", string(body))
	}
}

func TestRotatingWriterCleanupIntervalSkipsRepeatedScan(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "app.log")
	w := &rotatingWriter{
		filename:     file,
		maxFiles:     1,
		cleanupEvery: time.Hour,
	}

	base := time.Date(2026, 5, 17, 10, 0, 0, 0, time.Local)
	oldA := rotatedName(file, base.Add(-3*time.Hour))
	oldB := rotatedName(file, base.Add(-2*time.Hour))
	for _, name := range []string{oldA, oldB} {
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.cleanup(false); err != nil {
		t.Fatal(err)
	}

	oldC := rotatedName(file, base.Add(-1*time.Hour))
	if err := os.WriteFile(oldC, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := w.cleanup(false); err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(filepath.Join(dir, "app.*.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 {
		t.Fatalf("expected second cleanup to be skipped by interval, got %d files", len(matches))
	}
	if err := w.cleanup(true); err != nil {
		t.Fatal(err)
	}
	matches, err = filepath.Glob(filepath.Join(dir, "app.*.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected forced cleanup to keep one file, got %d", len(matches))
	}
}

func TestRotatingWriterCloseWaitsForCompression(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "app.log")
	w, err := newRotatingWriter(file, 6, "", 0, true, 0, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WriteLines([]string{"first", "second"}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "app.*.log.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("expected compressed rotated file to exist after Close")
	}
}

func TestRotatingWriterCompressionWaitTimeout(t *testing.T) {
	w := &rotatingWriter{closeTimeout: time.Nanosecond}
	w.beginCompression()
	defer w.finishCompression()

	if err := w.waitCompression(); err == nil {
		t.Fatal("expected compression wait timeout")
	}
}
