package feishu

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"time"
)

func openBrowser(url string) {
	var opener string
	switch runtime.GOOS {
	case "darwin":
		opener = "open"
	case "windows":
		opener = "start"
	default:
		opener = "xdg-open"
	}
	// 打开失败时仅依赖终端打印的链接，忽略错误
	_ = exec.Command(opener, url).Start()
}

// RunDeviceLogin OAuth 2.0 设备码流程登录：展示授权链接（并尝试打开浏览器），
// 轮询直到用户在飞书点「同意」。成功后把用户凭证写入本地存储并返回。
func RunDeviceLogin(ctx context.Context, auth *Auth, scope string) (*StoredUserToken, error) {
	device, err := auth.TokenClient.RequestDeviceAuthorization(ctx, scope)
	if err != nil {
		return nil, err
	}
	verifyURL := device.VerificationURIComplete
	if verifyURL == "" {
		verifyURL = device.VerificationURI
	}

	auth.Log.Info("请在【已登录飞书的浏览器 / 飞书客户端】中打开以下链接并点击「同意」授权:")
	auth.Log.Info("  " + verifyURL)
	if device.VerificationURIComplete == "" && device.UserCode != "" {
		auth.Log.Info("  用户码 user_code: " + device.UserCode)
	}
	auth.Log.Info("（正在尝试自动打开浏览器；若未打开请手动复制上面的链接）")
	openBrowser(verifyURL)

	deadline := auth.Clock().Add(time.Duration(device.ExpiresInSec) * time.Second)
	interval := time.Duration(device.IntervalSec) * time.Second

	for auth.Clock().Before(deadline) {
		time.Sleep(interval)
		result, err := auth.TokenClient.PollDeviceToken(ctx, device.DeviceCode)
		if err != nil {
			return nil, err
		}
		switch result.Status {
		case PollSuccess:
			return auth.Persist(result.Token, auth.Store.Read()), nil
		case PollSlowDown:
			interval += 5 * time.Second
		}
		auth.Log.Debug("等待用户授权中...")
	}

	return nil, fmt.Errorf("设备码已过期，用户未在有效期内完成授权，请重新运行 login")
}
