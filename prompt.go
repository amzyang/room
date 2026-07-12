package main

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/amzyang/room/output"
)

// interactive 是否允许交互式提问：stdin 是 TTY 且未要求机读输出
// （--json 是「机器在消费」的声明，即使在 pty 里也禁用交互）。
func (a *app) interactive() bool {
	return a.streams.InIsTerminal && !a.jsonOut
}

// prompter 终端交互问答（对齐 Node 版 readline 行为）。
type prompter struct {
	reader      *bufio.Reader
	out         io.Writer
	interactive bool
}

func (a *app) newPrompter() *prompter {
	return &prompter{
		reader:      bufio.NewReader(a.streams.In),
		out:         a.streams.Out,
		interactive: a.interactive(),
	}
}

// question 打印提示并读取一行输入（去首尾空白）。仅在交互环境下调用。
func (p *prompter) question(prompt string) string {
	fmt.Fprint(p.out, prompt)
	line, err := p.reader.ReadString('\n')
	if err != nil && line == "" {
		return ""
	}
	return strings.TrimSpace(line)
}

// questionRequired 必填交互项：非交互环境返回 validation 错误（hint 指明应传的 flag）。
func (p *prompter) questionRequired(prompt, missing, flagHint string) (string, error) {
	if !p.interactive {
		return "", output.Errf(output.TypeValidation, flagHint, "%s", missing)
	}
	return p.question(prompt), nil
}

// questionOptional 选填交互项：非交互环境静默返回空串（取默认值）。
func (p *prompter) questionOptional(prompt string) string {
	if !p.interactive {
		return ""
	}
	return p.question(prompt)
}
