package feishu

import (
	"testing"
	"time"
)

const regressionSign = "3WJ3Lmk2W7yEnMWQ+DTCzhcb6w3GSuXLPI87+UIdFPE="

func TestSignWebhook(t *testing.T) {
	if got := SignWebhook("testsecret", 1699999999); got != regressionSign {
		t.Errorf("SignWebhook regression vector = %q, want %q", got, regressionSign)
	}
	if SignWebhook("s", 1) == SignWebhook("s", 2) {
		t.Error("different timestamps should produce different signs")
	}
}

func TestBuildTextBody(t *testing.T) {
	body := BuildTextBody("hi", "", 0)
	if body.MsgType != "text" || body.Content.Text != "hi" || body.Timestamp != "" || body.Sign != "" {
		t.Errorf("no-secret body unexpected: %+v", body)
	}

	signed := BuildTextBody("hi", "testsecret", 1699999999)
	if signed.Timestamp != "1699999999" || signed.Sign != regressionSign {
		t.Errorf("signed body unexpected: %+v", signed)
	}
}

func makeWebhookClient(code int, msg string, legacy bool, secret string) (*WebhookClient, *[]WebhookTextBody) {
	var sent []WebhookTextBody
	client := &WebhookClient{
		URL:    "https://hook.test/x",
		Secret: secret,
		Post: func(_ string, body WebhookTextBody) (int, string, error) {
			sent = append(sent, body)
			return code, msg, nil
		},
		Clock: func() time.Time { return time.UnixMilli(1699999999000) },
	}
	_ = legacy
	return client, &sent
}

func TestWebhookClientSendText(t *testing.T) {
	client, sent := makeWebhookClient(0, "", false, "")
	if err := client.SendText("hello"); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	if (*sent)[0].Content.Text != "hello" || (*sent)[0].Sign != "" {
		t.Errorf("unexpected body: %+v", (*sent)[0])
	}

	signedClient, sent2 := makeWebhookClient(0, "", false, "testsecret")
	if err := signedClient.SendText("hello"); err != nil {
		t.Fatalf("SendText signed: %v", err)
	}
	if (*sent2)[0].Timestamp != "1699999999" || (*sent2)[0].Sign != regressionSign {
		t.Errorf("unexpected signed body: %+v", (*sent2)[0])
	}

	failClient, _ := makeWebhookClient(19021, "sign match fail", false, "")
	if err := failClient.SendText("x"); err == nil {
		t.Error("non-zero code should error")
	}
}
