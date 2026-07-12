package feishu

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetUserInfoSuccess(t *testing.T) {
	var gotMethod, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		fmt.Fprint(w, `{"code":0,"msg":"success","data":{"open_id":"ou_1","user_id":"u_1","name":"张三"}}`)
	}))
	defer server.Close()

	client := &OAuthClient{HTTP: &http.Client{}, AppID: "a", AppSecret: "s", userInfoURL: server.URL}
	got, err := client.GetUserInfo(context.Background(), "at-token")
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotAuth != "Bearer at-token" {
		t.Errorf("Authorization = %q, want Bearer at-token", gotAuth)
	}
	want := UserIdentity{OpenID: "ou_1", UserID: "u_1", Name: "张三"}
	if *got != want {
		t.Errorf("identity = %+v, want %+v", *got, want)
	}
}

func TestGetUserInfoMissingUserID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"code":0,"msg":"success","data":{"open_id":"ou_1","name":"张三"}}`)
	}))
	defer server.Close()

	client := &OAuthClient{HTTP: &http.Client{}, userInfoURL: server.URL}
	got, err := client.GetUserInfo(context.Background(), "at")
	if err != nil {
		t.Fatal(err)
	}
	if got.UserID != "" || got.OpenID != "ou_1" {
		t.Errorf("identity = %+v, want UserID 为空且 OpenID=ou_1", *got)
	}
}

func TestGetUserInfoAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"code":99991668,"msg":"token invalid"}`)
	}))
	defer server.Close()

	client := &OAuthClient{HTTP: &http.Client{}, userInfoURL: server.URL}
	_, err := client.GetUserInfo(context.Background(), "bad")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != 99991668 {
		t.Errorf("err = %v, want APIError code 99991668", err)
	}
}

func TestGetUserInfoTransportError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.Close()

	client := &OAuthClient{HTTP: &http.Client{}, userInfoURL: server.URL}
	if _, err := client.GetUserInfo(context.Background(), "at"); err == nil {
		t.Error("want transport error")
	}
}
