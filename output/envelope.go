// Package output 提供 agent 友好的机读输出层：
// 成功信封 {ok:true,data,meta} 写 stdout，错误信封 {ok:false,error} 写 stderr，
// 错误类型映射到稳定退出码闭集（0/1/2/3/10）。
package output

import (
	"encoding/json"
	"io"
)

// Envelope 机读输出信封。消费者只依赖 ok 字段与退出码判断成败。
type Envelope struct {
	OK   bool  `json:"ok"`
	Data any   `json:"data,omitempty"`
	Meta *Meta `json:"meta,omitempty"`
}

// Meta 信封元数据（列表计数、演练标记）。
type Meta struct {
	Count  int  `json:"count"`
	DryRun bool `json:"dry_run,omitempty"`
}

// WriteSuccess 向 w 写一行成功信封（NDJSON 友好）。
func WriteSuccess(w io.Writer, data any, meta *Meta) error {
	return json.NewEncoder(w).Encode(Envelope{OK: true, Data: data, Meta: meta})
}
