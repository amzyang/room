package envutil

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// EnvPair 一条待写入 .env 的 KEY=VALUE。
type EnvPair struct {
	Key   string
	Value string
}

// UpsertEnvFile 原位更新 path 中目标 KEY 的赋值行，其余行（注释/空行/顺序）逐字节保留。
// 匹配 `export KEY=`、`KEY =` 等变体；重复 KEY 行全部替换（godotenv 取后行，漏改会被覆盖）；
// 无匹配则追加到文件末尾。文件不存在时创建为 0600（写入的是凭证），已存在则保留原权限。
// 仅处理单行 KEY=VALUE，不支持 godotenv 的多行引号值语法。
func UpsertEnvFile(path string, pairs []EnvPair) error {
	content := ""
	mode := os.FileMode(0o600)
	data, err := os.ReadFile(path)
	if err == nil {
		content = string(data)
		if info, statErr := os.Stat(path); statErr == nil {
			mode = info.Mode().Perm()
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	for _, pair := range pairs {
		content = upsertEnvLine(content, pair)
	}
	return WriteFileAtomic(path, []byte(content), mode)
}

// WriteFileAtomic 同目录临时文件 + rename：写入中断（kill/断电/磁盘满）不会把原文件截断。
func WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func upsertEnvLine(content string, pair EnvPair) string {
	line := pair.Key + "=" + quoteEnvValue(pair.Value)
	pattern := regexp.MustCompile(`^\s*(export\s+)?` + regexp.QuoteMeta(pair.Key) + `\s*=`)

	lines := strings.Split(content, "\n")
	replaced := false
	for i, l := range lines {
		if pattern.MatchString(l) {
			lines[i] = line
			replaced = true
		}
	}
	if replaced {
		return strings.Join(lines, "\n")
	}
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return content + line + "\n"
}

var bareEnvValue = regexp.MustCompile(`^[A-Za-z0-9_./:@+-]*$`)

// quoteEnvValue 飞书 app_id/app_secret 这类安全字符集的值裸写（与 .env.example 风格一致），
// 其余加双引号并转义 \、" 与 $（godotenv 会展开双引号值内未转义的 $VAR），
// 保证 godotenv/CleanEnvValue 读取往返一致。
func quoteEnvValue(v string) string {
	if bareEnvValue.MatchString(v) {
		return v
	}
	escaped := strings.ReplaceAll(v, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	escaped = strings.ReplaceAll(escaped, `$`, `\$`)
	return `"` + escaped + `"`
}
