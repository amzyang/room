package feishu

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"time"
)

// ErrLoginExpired 设备码过期或用户未在有效期内完成授权。
var ErrLoginExpired = errors.New("设备码已过期，用户未在有效期内完成授权，请重新运行 login")

// OpenBrowser 尽力用系统默认浏览器打开 url；失败时仅依赖终端打印的链接，忽略错误。
func OpenBrowser(url string) {
	var opener string
	switch runtime.GOOS {
	case "darwin":
		opener = "open"
	case "windows":
		opener = "start"
	default:
		opener = "xdg-open"
	}
	_ = exec.Command(opener, url).Start()
}

// PollDeviceLogin 轮询设备码授权结果直到用户点「同意」，成功后把用户凭证写入
// 本地存储并返回。发起授权（RequestDeviceAuthorization）由调用方负责，
// 以支持 --no-wait / --device-code 两段式。
func PollDeviceLogin(ctx context.Context, auth *Auth, deviceCode string, intervalSec, expiresInSec int) (*StoredUserToken, error) {
	return pollDeviceLogin(ctx, auth, deviceCode, intervalSec, expiresInSec, nil)
}

func pollDeviceLogin(ctx context.Context, auth *Auth, deviceCode string, intervalSec, expiresInSec int,
	sleep func(context.Context, time.Duration) error) (*StoredUserToken, error) {
	deadline := auth.Clock().Add(time.Duration(expiresInSec) * time.Second)
	interval := time.Duration(intervalSec) * time.Second

	for auth.Clock().Before(deadline) {
		if err := waitFor(ctx, sleep, interval); err != nil {
			return nil, err
		}
		result, err := auth.TokenClient.PollDeviceToken(ctx, deviceCode)
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

	return nil, ErrLoginExpired
}
