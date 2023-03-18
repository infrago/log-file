package log_file

import (
	"bufio"
	"io"
	"os"
)

const (
	MAX_LEVEL = 999

	SLICE_NULL  = ""
	SLICE_YEAR  = "year"
	SLICE_MONTH = "month"
	SLICE_DAY   = "day"
	SLICE_HOUR  = "hour"
)

// checkSlice 获取日期分片类型
func checkSlice(s string) string {
	switch s {
	case "year", "y", "nian":
		return SLICE_YEAR
	case "month", "m", "yue":
		return SLICE_MONTH
	case "day", "d", "tian":
		return SLICE_DAY
	case "hour", "h", "shi":
		return SLICE_HOUR
	default:
		return SLICE_DAY
	}
}

// create file
func createFile(filename string) error {
	newFile, err := os.Create(filename)
	defer newFile.Close()
	return err
}

// pathExists 判断目录是否存在
func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// getFileLines 获取文件行数
func getFileLines(filename string) (fileLine int64, err error) {
	file, err := os.OpenFile(filename, os.O_RDONLY, 0766)
	if err != nil {
		return fileLine, err
	}
	defer file.Close()

	fileLine = 1
	r := bufio.NewReader(file)
	for {
		_, err := r.ReadString('\n')
		if err != nil || err == io.EOF {
			break
		}
		fileLine += 1
	}
	return fileLine, nil
}
