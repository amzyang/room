package output

import (
	"io"
	"os"

	"golang.org/x/term"
)

// Streams 进程标准流与一次性 TTY 检测结果。测试直接构造并置布尔字段。
type Streams struct {
	In            io.Reader
	Out           io.Writer
	Err           io.Writer
	InIsTerminal  bool
	OutIsTerminal bool
}

// DetectStreams 绑定进程标准流并检测 TTY。
func DetectStreams() *Streams {
	return &Streams{
		In:            os.Stdin,
		Out:           os.Stdout,
		Err:           os.Stderr,
		InIsTerminal:  term.IsTerminal(int(os.Stdin.Fd())),
		OutIsTerminal: term.IsTerminal(int(os.Stdout.Fd())),
	}
}
