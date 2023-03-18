package log_file

import (
	"os"
	"path"
	"strings"
	"sync"
	"time"
)

// FileWriter
type FileWriter struct {
	connect   *fileConnect
	lock      sync.RWMutex
	writer    *os.File
	startLine int64
	startTime int64
	filename  string
}

// newFileWriter
func newFileWriter(c *fileConnect, fn string) *FileWriter {
	return &FileWriter{
		connect: c, filename: fn,
	}
}

// init 初始化文件
func (fw *FileWriter) init() error {

	// 判断文件是否存在，不存在则创建
	ok, _ := pathExists(fw.filename)
	if ok == false {
		err := createFile(fw.filename)
		if err != nil {
			return err
		}
	}

	//开始时间
	fw.startTime = time.Now().Unix()

	// 开始行
	nowLines, err := getFileLines(fw.filename)
	if err != nil {
		return err
	}
	fw.startLine = nowLines

	file, err := fw.getFileObject(fw.filename)
	if err != nil {
		return err
	}
	fw.writer = file
	return nil
}

// write by config
func (fw *FileWriter) write(msg string) error {

	fw.lock.Lock()
	defer fw.lock.Unlock()

	if fw.connect.setting.DateSlice != "" {
		// 检测日期分片
		err := fw.sliceByDate(fw.connect.setting.DateSlice)
		if err != nil {
			return err
		}
	}
	if fw.connect.setting.MaxLine != 0 {
		// 检测行数分片
		err := fw.sliceByFileLines(fw.connect.setting.MaxLine)
		if err != nil {
			return err
		}
	}
	if fw.connect.setting.MaxSize != 0 {
		// 检测文件大小分片
		err := fw.sliceByFileSize(fw.connect.setting.MaxSize)
		if err != nil {
			return err
		}
	}

	fw.writer.Write([]byte(msg + "\n"))
	if fw.connect.setting.MaxLine != 0 {
		fw.startLine += int64(strings.Count(msg, "\n"))
	}
	return nil
}

// sliceByDate 按日期分片
func (fw *FileWriter) sliceByDate(dataSlice string) error {

	filename := fw.filename
	filenameSuffix := path.Ext(filename)
	startTime := time.Unix(fw.startTime, 0)
	nowTime := time.Now()

	oldFilename := ""
	isHaveSlice := false
	if (dataSlice == SLICE_YEAR) &&
		(startTime.Year() != nowTime.Year()) {
		isHaveSlice = true
		oldFilename = strings.Replace(filename, filenameSuffix, "", 1) + "_" + startTime.Format("06") + filenameSuffix
	}
	if (dataSlice == SLICE_MONTH) &&
		(startTime.Format("0601") != nowTime.Format("0601")) {
		isHaveSlice = true
		oldFilename = strings.Replace(filename, filenameSuffix, "", 1) + "_" + startTime.Format("0601") + filenameSuffix
	}
	if (dataSlice == SLICE_DAY) &&
		(startTime.Format("060102") != nowTime.Format("060102")) {
		isHaveSlice = true
		oldFilename = strings.Replace(filename, filenameSuffix, "", 1) + "_" + startTime.Format("060102") + filenameSuffix
	}
	if (dataSlice == SLICE_HOUR) &&
		(startTime.Format("06010215") != startTime.Format("06010215")) {
		isHaveSlice = true
		oldFilename = strings.Replace(filename, filenameSuffix, "", 1) + "_" + startTime.Format("06010215") + filenameSuffix
	}

	if isHaveSlice == true {
		//关闭文件
		fw.writer.Close()
		err := os.Rename(fw.filename, oldFilename)
		if err != nil {
			return err
		}
		err = fw.init()
		if err != nil {
			return err
		}
	}

	return nil
}

// sliceByFileLines 按文件行数分片，如果触发分片，当前文件会被重命名
// 重命名规则见下面代码
func (fw *FileWriter) sliceByFileLines(maxLine int64) error {

	filename := fw.filename
	filenameSuffix := path.Ext(filename)
	startLine := fw.startLine

	if startLine >= maxLine {
		//关闭文件
		fw.writer.Close()
		timeFlag := time.Now().Format("060102.150405")
		oldFilename := strings.Replace(filename, filenameSuffix, "", 1) + "." + timeFlag + filenameSuffix
		err := os.Rename(filename, oldFilename)
		if err != nil {
			return err
		}
		err = fw.init()
		if err != nil {
			return err
		}
	}

	return nil
}

// sliceByFileSize 按文件大小分片，如果触发分片，当前文件会被重命名
// 重命名规则见下面代码
func (fw *FileWriter) sliceByFileSize(maxSize int64) error {

	filename := fw.filename
	filenameSuffix := path.Ext(filename)
	nowSize, _ := fw.getFileSize(filename)

	if nowSize >= maxSize {
		//关闭文件
		fw.writer.Close()
		timeFlag := time.Now().Format("060102.150405")
		oldFilename := strings.Replace(filename, filenameSuffix, "", 1) + "." + timeFlag + filenameSuffix
		err := os.Rename(filename, oldFilename)
		if err != nil {
			return err
		}
		err = fw.init()
		if err != nil {
			return err
		}
	}

	return nil
}

// getFileObject
func (fw *FileWriter) getFileObject(filename string) (file *os.File, err error) {
	file, err = os.OpenFile(filename, os.O_RDWR|os.O_APPEND, 0766)
	return file, err
}

// getFileSize
func (fw *FileWriter) getFileSize(filename string) (fileSize int64, err error) {
	fileInfo, err := os.Stat(filename)
	if err != nil {
		return fileSize, err
	}

	return fileInfo.Size(), nil
}
