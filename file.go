package log_file

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bamgoo/bamgoo"
	. "github.com/bamgoo/base"
	blog "github.com/bamgoo/log"
)

type (
	fileDriver struct{}

	fileConnection struct {
		instance *blog.Instance
		setting  fileSetting
		writers  map[blog.Level]*rotatingWriter
	}

	fileSetting struct {
		store      string
		output     string
		levelFiles map[blog.Level]string
		maxSize    int64
		slice      string
		maxLine    int64
	}
)

const outputBucket blog.Level = blog.LevelDebug + 1

func init() {
	bamgoo.Register("file", &fileDriver{})
}

func (d *fileDriver) Connect(inst *blog.Instance) (blog.Connect, error) {
	setting := fileSetting{
		store:      "logs",
		output:     "",
		levelFiles: map[blog.Level]string{},
		maxSize:    100 * 1024 * 1024,
		slice:      "",
		maxLine:    0,
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
	if c.setting.output != "" {
		path := c.resolvePath(c.setting.output)
		w, err := newRotatingWriter(path, c.setting.maxSize, c.setting.slice, c.setting.maxLine)
		if err != nil {
			return err
		}
		c.writers[outputBucket] = w
	}

	for level, file := range c.setting.levelFiles {
		path := c.resolvePath(file)
		w, err := newRotatingWriter(path, c.setting.maxSize, c.setting.slice, c.setting.maxLine)
		if err != nil {
			return err
		}
		c.writers[level] = w
	}
	return nil
}

func (c *fileConnection) Close() error {
	var closeErr error
	for _, writer := range c.writers {
		if err := writer.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (c *fileConnection) Write(logs ...blog.Log) error {
	for _, entry := range logs {
		line := c.instance.Format(entry)

		if writer, ok := c.writers[outputBucket]; ok {
			if err := writer.WriteLine(line); err != nil {
				return err
			}
		}
		if writer, ok := c.writers[entry.Level]; ok {
			if err := writer.WriteLine(line); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *fileConnection) resolvePath(file string) string {
	if filepath.IsAbs(file) {
		return file
	}
	return filepath.Join(c.setting.store, file)
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

func rotatedName(filename string, now time.Time) string {
	ext := filepath.Ext(filename)
	base := strings.TrimSuffix(filename, ext)
	return fmt.Sprintf("%s.%s%s", base, now.Format("20060102.150405"), ext)
}
