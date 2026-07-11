package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// prompter 终端交互问答（对齐 Node 版 readline 行为）。
type prompter struct {
	reader *bufio.Reader
}

func newPrompter() *prompter {
	return &prompter{reader: bufio.NewReader(os.Stdin)}
}

// question 打印提示并读取一行输入（去首尾空白）。
func (p *prompter) question(prompt string) string {
	fmt.Print(prompt)
	line, err := p.reader.ReadString('\n')
	if err != nil && line == "" {
		return ""
	}
	return strings.TrimSpace(line)
}
