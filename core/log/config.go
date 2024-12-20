package log

import (
	"errors"
	"github.com/1Panel-dev/1Panel/core/constant"
	"io"
	"os"
	"path"
)

var (
	BufferSize         = 0x100000
	DefaultFileMode    = os.FileMode(constant.DirPerm)
	DefaultFileFlag    = os.O_RDWR | os.O_CREATE | os.O_APPEND
	ErrInvalidArgument = errors.New("error argument invalid")
	QueueSize          = 1024
	ErrClosed          = errors.New("error write on close")
)

type Config struct {
	TimeTagFormat      string
	LogPath            string
	FileName           string
	LogSuffix          string
	MaxRemain          int
	RollingTimePattern string
}

type Manager interface {
	Fire() chan string
	Close()
}

type RollingWriter interface {
	io.Writer
	Close() error
}

func FilePath(c *Config) (filepath string) {
	filepath = path.Join(c.LogPath, c.FileName) + c.LogSuffix
	return
}
