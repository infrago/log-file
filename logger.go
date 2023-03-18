package log_file

import (
	"os"
	"path"
	"strings"
	"time"

	"github.com/infrago/log"
	"github.com/infrago/util"
)

type (
	fileDriver struct {
		//store 驱动默认的存储路径
		store string
	}
	fileConnect struct {
		instance *log.Instance

		setting fileSetting
		writers map[log.Level]*FileWriter
	}
	fileSetting struct {
		//File 默认日志文件
		File string
		//LevelFiles 不同级别的日志文件
		LevelFiles map[log.Level]string
		// MaxSize 日志文件最大尺寸
		MaxSize int64
		//MaxLine 日志文件最大行
		MaxLine int64

		// DateSlice 日志文件按日期分片
		// 具体参考 checkSlice 方法
		DateSlice string
	}
)

func (driver *fileDriver) Connect(inst *log.Instance) (log.Connect, error) {
	//默认路径
	store := driver.store
	if vv, ok := inst.Config.Setting["store"].(string); ok && vv != "" {
		store = vv
	}

	_, e := os.Stat(store)
	if e != nil {
		//创建目录，如果不存在
		os.MkdirAll(store, 0700)
	}

	// 默认setting
	setting := fileSetting{
		LevelFiles: make(map[log.Level]string, 0),
		MaxSize:    1024 * 1024 * 100,
		MaxLine:    1000000,
		DateSlice:  "day",
	}

	levels := log.Levels()
	for level, name := range levels {
		key := strings.ToLower(name)
		file := key + ".log"
		if vv, ok := inst.Config.Setting[key].(string); ok && vv != "" {
			setting.LevelFiles[level] = vv
		} else if vv, ok := inst.Config.Setting[key].(bool); ok && vv {
			setting.LevelFiles[level] = path.Join(store, file)
		} else {
			setting.LevelFiles[level] = path.Join(store, file)
		}
	}

	if vv, ok := inst.Config.Setting["output"].(string); ok && vv != "" {
		setting.File = vv
	} else if vv, ok := inst.Config.Setting["output"].(bool); ok && vv {
		setting.File = path.Join(store, "output.log")
	}

	//maxsize
	if vv, ok := inst.Config.Setting["maxsize"].(string); ok && vv != "" {
		size := util.ParseSize(vv)
		if size > 0 {
			setting.MaxSize = size
		}
	} else if vv, ok := inst.Config.Setting["maxsize"].(int64); ok && vv > 0 {
		setting.MaxSize = vv
	} else if vv, ok := inst.Config.Setting["weight"].(int64); ok && vv > 0 {
		setting.MaxSize = vv
	}

	//maxline
	if vv, ok := inst.Config.Setting["maxline"].(int64); ok && vv > 0 {
		setting.MaxLine = vv
	} else if vv, ok := inst.Config.Setting["height"].(int64); ok && vv > 0 {
		setting.MaxLine = vv
	}

	if vv, ok := inst.Config.Setting["slice"].(string); ok && vv != "" {
		setting.DateSlice = checkSlice(vv)
	}

	return &fileConnect{
		instance: inst, setting: setting,
	}, nil
}

// 打开连接
func (this *fileConnect) Open() error {

	writers := make(map[log.Level]*FileWriter, 0)
	if len(this.setting.LevelFiles) > 0 {
		for level, filename := range this.setting.LevelFiles {
			writer := newFileWriter(this, filename)
			writer.init()
			writers[level] = writer
		}
	}
	if this.setting.File != "" {
		writer := newFileWriter(this, this.setting.File)
		writer.init()
		writers[MAX_LEVEL] = writer
	}

	this.writers = writers

	return nil
}

// 关闭连接
func (this *fileConnect) Close() error {
	//为了最后一条日志能正常输出，延迟一小会
	time.Sleep(time.Microsecond * 100)
	this.Flush()
	return nil
}

// Write 写日志
// 可以考虑换成封闭好的协程库来执行并行任务
// 老代码搬运，暂时先这样
func (this *fileConnect) Write(log log.Log) error {

	msg := this.instance.Format(log)

	var accessChan = make(chan error, 1)
	var levelChan = make(chan error, 1)

	if this.setting.File != "" {
		go func() {
			accessFileWrite, ok := this.writers[MAX_LEVEL]
			if !ok {
				accessChan <- nil
				return
			}
			err := accessFileWrite.write(msg)
			if err != nil {
				accessChan <- err
				return
			}
			accessChan <- nil
		}()
	}

	if len(this.setting.LevelFiles) != 0 {
		go func() {
			fileWrite, ok := this.writers[log.Level]
			if !ok {
				levelChan <- nil
				return
			}
			err := fileWrite.write(msg)
			if err != nil {
				levelChan <- err
				return
			}
			levelChan <- nil
		}()
	}

	var accessErr error
	var levelErr error
	if this.setting.File != "" {
		accessErr = <-accessChan
	}
	if len(this.setting.LevelFiles) != 0 {
		levelErr = <-levelChan
	}
	if accessErr != nil {
		return accessErr.(error)
	}
	if levelErr != nil {
		return levelErr.(error)
	}
	return nil
}

func (this *fileConnect) Flush() {
	for _, writer := range this.writers {
		writer.writer.Close()
	}
}
