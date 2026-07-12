package output

// 退出码闭集。核心不变式：exit 0 ⟺ 命令的后置条件达成（如 book ⟺ 房间订上了）。
const (
	ExitOK                   = 0
	ExitAPI                  = 1  // API/业务失败（含 book 未订到，细分靠 error.type）
	ExitValidation           = 2  // 参数/输入校验失败（含非交互环境缺必要 flag）
	ExitAuth                 = 3  // 认证或配置缺失
	ExitConfirmationRequired = 10 // 需显式确认（加 --yes / --force）
)

// ExitCode 错误 → 退出码。nil 返回 0。
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	switch Classify(err).Type {
	case TypeValidation:
		return ExitValidation
	case TypeAuth, TypeConfig:
		return ExitAuth
	case TypeConfirmationRequired:
		return ExitConfirmationRequired
	default:
		return ExitAPI
	}
}
