package log_file

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	. "github.com/infrago/base"
	"github.com/infrago/infra"
	blog "github.com/infrago/log"
)

type (
	fileDriver struct{}

	fileConnection struct {
		instance    *blog.Instance
		setting     fileSetting
		writers     map[blog.Level]*rotatingWriter
		writerIndex map[*rotatingWriter]int
		writerList  []*rotatingWriter
		lineGroups  sync.Pool
	}

	fileSetting struct {
		store        string
		output       string
		levelFiles   map[blog.Level]string
		maxSize      int64
		slice        string
		maxLine      int64
		compress     bool
		maxFiles     int
		maxAge       time.Duration
		closeTimeout time.Duration
		flushEvery   time.Duration
		cleanupEvery time.Duration
	}
)

const outputBucket blog.Level = blog.LevelDebug + 1

func init() {
	infra.Register("file", &fileDriver{})
}

func (d *fileDriver) Connect(inst *blog.Instance) (blog.Connection, error) {
	setting := fileSetting{
		store:        "store/log",
		output:       "",
		levelFiles:   map[blog.Level]string{},
		maxSize:      100 * 1024 * 1024,
		slice:        "",
		maxLine:      0,
		compress:     false,
		maxFiles:     0,
		maxAge:       0,
		closeTimeout: 0,
		flushEvery:   0,
		cleanupEvery: 0,
	}

	if v, ok := getString(inst.Setting, "store"); ok && v != "" {
		setting.store = v
	}
	if v, ok := getString(inst.Setting, "output"); ok && v != "" {
		setting.output = v
	}
	if v, ok := getBool(inst.Setting, "output"); ok && v && setting.output == "" {
		setting.output = "output.log"
	}

	if v, ok := getString(inst.Setting, "maxsize"); ok && v != "" {
		if size, ok := parseSize(v); ok && size > 0 {
			setting.maxSize = size
		}
	}
	if v, ok := getInt64(inst.Setting, "maxsize"); ok && v > 0 {
		setting.maxSize = v
	}
	if v, ok := getString(inst.Setting, "slice"); ok {
		setting.slice = normalizeSlice(v)
	}
	if v, ok := getInt64(inst.Setting, "maxline"); ok && v > 0 {
		setting.maxLine = v
	}
	if v, ok := getInt64(inst.Setting, "maxfiles"); ok && v > 0 {
		setting.maxFiles = int(v)
	}
	if v, ok := getString(inst.Setting, "maxage"); ok && v != "" {
		if d, ok := parseAge(v); ok && d > 0 {
			setting.maxAge = d
		}
	}
	if v, ok := getInt64(inst.Setting, "maxage"); ok && v > 0 {
		setting.maxAge = time.Second * time.Duration(v)
	}
	if v, ok := getBool(inst.Setting, "compress"); ok {
		setting.compress = v
	}
	if v, ok := getString(inst.Setting, "close_timeout"); ok && v != "" {
		if d, ok := parseAge(v); ok && d > 0 {
			setting.closeTimeout = d
		}
	}
	if v, ok := getString(inst.Setting, "closetimeout"); ok && v != "" {
		if d, ok := parseAge(v); ok && d > 0 {
			setting.closeTimeout = d
		}
	}
	if v, ok := getInt64(inst.Setting, "close_timeout"); ok && v > 0 {
		setting.closeTimeout = time.Second * time.Duration(v)
	}
	if v, ok := getInt64(inst.Setting, "closetimeout"); ok && v > 0 {
		setting.closeTimeout = time.Second * time.Duration(v)
	}
	if v, ok := getString(inst.Setting, "flush_interval"); ok && v != "" {
		if d, ok := parseAge(v); ok && d > 0 {
			setting.flushEvery = d
		}
	}
	if v, ok := getString(inst.Setting, "flushinterval"); ok && v != "" {
		if d, ok := parseAge(v); ok && d > 0 {
			setting.flushEvery = d
		}
	}
	if v, ok := getInt64(inst.Setting, "flush_interval"); ok && v > 0 {
		setting.flushEvery = time.Second * time.Duration(v)
	}
	if v, ok := getInt64(inst.Setting, "flushinterval"); ok && v > 0 {
		setting.flushEvery = time.Second * time.Duration(v)
	}
	if v, ok := getString(inst.Setting, "cleanup_interval"); ok && v != "" {
		if d, ok := parseAge(v); ok && d > 0 {
			setting.cleanupEvery = d
		}
	}
	if v, ok := getString(inst.Setting, "cleanupinterval"); ok && v != "" {
		if d, ok := parseAge(v); ok && d > 0 {
			setting.cleanupEvery = d
		}
	}
	if v, ok := getInt64(inst.Setting, "cleanup_interval"); ok && v > 0 {
		setting.cleanupEvery = time.Second * time.Duration(v)
	}
	if v, ok := getInt64(inst.Setting, "cleanupinterval"); ok && v > 0 {
		setting.cleanupEvery = time.Second * time.Duration(v)
	}

	levels := blog.Levels()
	for level, name := range levels {
		key := strings.ToLower(name)
		if path, ok := getString(inst.Setting, key); ok && path != "" {
			setting.levelFiles[level] = path
			continue
		}
		if enabled, ok := getBool(inst.Setting, key); ok && enabled {
			setting.levelFiles[level] = key + ".log"
		}
	}

	if setting.output == "" && len(setting.levelFiles) == 0 {
		setting.output = "output.log"
	}

	return &fileConnection{
		instance: inst,
		setting:  setting,
		writers:  map[blog.Level]*rotatingWriter{},
	}, nil
}

func (c *fileConnection) Open() error {
	byPath := map[string]*rotatingWriter{}
	c.writerIndex = map[*rotatingWriter]int{}
	c.writerList = nil
	registerWriter := func(writer *rotatingWriter) {
		if _, ok := c.writerIndex[writer]; ok {
			return
		}
		c.writerIndex[writer] = len(c.writerList)
		c.writerList = append(c.writerList, writer)
	}
	openWriter := func(file string) (*rotatingWriter, error) {
		path := c.resolvePath(file)
		key := canonicalPath(path)
		if writer, ok := byPath[key]; ok {
			return writer, nil
		}
		writer, err := newRotatingWriter(path, c.setting.maxSize, c.setting.slice, c.setting.maxLine, c.setting.compress, c.setting.maxFiles, c.setting.maxAge, c.setting.closeTimeout, c.setting.flushEvery, c.setting.cleanupEvery)
		if err != nil {
			_ = c.Close()
			return nil, err
		}
		byPath[key] = writer
		registerWriter(writer)
		return writer, nil
	}

	if c.setting.output != "" {
		w, err := openWriter(c.setting.output)
		if err != nil {
			return err
		}
		c.writers[outputBucket] = w
	}

	for level, file := range c.setting.levelFiles {
		w, err := openWriter(file)
		if err != nil {
			return err
		}
		c.writers[level] = w
	}
	return nil
}

func (c *fileConnection) Close() error {
	var closeErr error
	closed := map[*rotatingWriter]bool{}
	for _, writer := range c.writers {
		if closed[writer] {
			continue
		}
		closed[writer] = true
		if err := writer.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (c *fileConnection) Write(logs ...blog.Log) error {
	if len(logs) == 0 {
		return nil
	}
	linesPtr := c.getLineGroups()
	linesByWriter := *linesPtr
	defer c.putLineGroups(linesPtr)
	outputWriter := c.writers[outputBucket]
	outputIndex := -1
	if outputWriter != nil {
		outputIndex = c.writerIndex[outputWriter]
	}
	for _, entry := range logs {
		line := c.instance.Format(entry)
		if outputIndex >= 0 {
			linesByWriter[outputIndex] = append(linesByWriter[outputIndex], line)
		}
		if writer, ok := c.writers[entry.Level]; ok && writer != outputWriter {
			idx := c.writerIndex[writer]
			linesByWriter[idx] = append(linesByWriter[idx], line)
		}
	}
	for idx, lines := range linesByWriter {
		if len(lines) == 0 {
			continue
		}
		if err := c.writerList[idx].WriteLines(lines); err != nil {
			return err
		}
	}
	return nil
}

func (c *fileConnection) getLineGroups() *[][]string {
	if value := c.lineGroups.Get(); value != nil {
		linesPtr := value.(*[][]string)
		lines := *linesPtr
		if cap(lines) >= len(c.writerList) {
			lines = lines[:len(c.writerList)]
			for i := range lines {
				lines[i] = lines[i][:0]
			}
			*linesPtr = lines
			return linesPtr
		}
	}
	lines := make([][]string, len(c.writerList))
	return &lines
}

func (c *fileConnection) putLineGroups(linesPtr *[][]string) {
	lines := *linesPtr
	for i := range lines {
		for j := range lines[i] {
			lines[i][j] = ""
		}
		if cap(lines[i]) > 4096 {
			lines[i] = nil
			continue
		}
		lines[i] = lines[i][:0]
	}
	if cap(lines) > 64 {
		return
	}
	*linesPtr = lines[:0]
	c.lineGroups.Put(linesPtr)
}

func (c *fileConnection) resolvePath(file string) string {
	if filepath.IsAbs(file) {
		return file
	}
	return filepath.Join(c.setting.store, file)
}

func canonicalPath(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		if abs, err := filepath.Abs(real); err == nil {
			return abs
		}
		return real
	}
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	if realDir, err := filepath.EvalSymlinks(dir); err == nil {
		path = filepath.Join(realDir, base)
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

func getString(m Map, key string) (string, bool) {
	if m == nil {
		return "", false
	}
	value, ok := m[key]
	if !ok {
		return "", false
	}
	v, ok := value.(string)
	return v, ok
}

func getBool(m Map, key string) (bool, bool) {
	if m == nil {
		return false, false
	}
	value, ok := m[key]
	if !ok {
		return false, false
	}
	v, ok := value.(bool)
	return v, ok
}

func getInt64(m Map, key string) (int64, bool) {
	if m == nil {
		return 0, false
	}
	value, ok := m[key]
	if !ok {
		return 0, false
	}
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		return int64(v), true
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		if err == nil {
			return n, true
		}
	}
	return 0, false
}

func normalizeSlice(slice string) string {
	switch strings.ToLower(slice) {
	case "year", "y":
		return "year"
	case "month", "m":
		return "month"
	case "day", "d":
		return "day"
	case "hour", "h":
		return "hour"
	default:
		return ""
	}
}

func parseSize(raw string) (int64, bool) {
	value := strings.TrimSpace(strings.ToUpper(raw))
	if value == "" {
		return 0, false
	}

	units := []struct {
		suffix string
		scale  int64
	}{
		{"GB", 1024 * 1024 * 1024},
		{"G", 1024 * 1024 * 1024},
		{"MB", 1024 * 1024},
		{"M", 1024 * 1024},
		{"KB", 1024},
		{"K", 1024},
		{"B", 1},
	}

	for _, unit := range units {
		if strings.HasSuffix(value, unit.suffix) {
			number := strings.TrimSpace(strings.TrimSuffix(value, unit.suffix))
			if number == "" {
				return 0, false
			}
			f, err := strconv.ParseFloat(number, 64)
			if err != nil {
				return 0, false
			}
			return int64(f * float64(unit.scale)), true
		}
	}

	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func parseAge(raw string) (time.Duration, bool) {
	value := strings.TrimSpace(strings.ToLower(raw))
	if value == "" {
		return 0, false
	}
	if d, err := time.ParseDuration(value); err == nil {
		return d, true
	}

	units := []struct {
		suffix string
		scale  time.Duration
	}{
		{"w", 7 * 24 * time.Hour},
		{"d", 24 * time.Hour},
		{"h", time.Hour},
		{"m", time.Minute},
		{"s", time.Second},
	}
	for _, unit := range units {
		if strings.HasSuffix(value, unit.suffix) {
			number := strings.TrimSpace(strings.TrimSuffix(value, unit.suffix))
			if number == "" {
				return 0, false
			}
			n, err := strconv.ParseFloat(number, 64)
			if err != nil {
				return 0, false
			}
			return time.Duration(n * float64(unit.scale)), true
		}
	}
	return 0, false
}

func rotatedName(filename string, now time.Time) string {
	ext := filepath.Ext(filename)
	base := strings.TrimSuffix(filename, ext)
	return fmt.Sprintf("%s.%s%s", base, now.Format("20060102.150405.000000000"), ext)
}

func nextRotatedName(filename string, now time.Time) (string, error) {
	for i := 0; ; i++ {
		candidate := rotatedName(filename, now.Add(time.Duration(i)))
		if _, err := os.Stat(candidate); err != nil {
			if os.IsNotExist(err) {
				return candidate, nil
			}
			return "", err
		}
	}
}
