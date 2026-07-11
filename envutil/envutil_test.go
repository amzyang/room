package envutil

import (
	"reflect"
	"testing"
)

func TestCleanEnvValue(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`"hello"`, "hello"},
		{`'world'`, "world"},
		{" hello ", "hello"},
		{`" hello "`, "hello"},
		{`' world '`, "world"},
		{` "test" `, "test"},
		{"", ""},
		{`""`, ""},
		{`''`, ""},
		{`"hello world"`, "hello world"},
		{`"项目 周会"`, "项目 周会"},
		{`"fri"`, "fri"},
		{`"项目周会"`, "项目周会"},
		{`"app_id_12345"`, "app_id_12345"},
	}
	for _, c := range cases {
		if got := CleanEnvValue(c.in); got != c.want {
			t.Errorf("CleanEnvValue(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseEnvList(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`"a,b,c"`, []string{"a", "b", "c"}},
		{`"沟通力,亲和力,学习力"`, []string{"沟通力", "亲和力", "学习力"}},
		{"a,b,c", []string{"a", "b", "c"}},
		{"a, b, c", []string{"a", "b", "c"}},
		{`" a , b , c "`, []string{"a", "b", "c"}},
		{"", nil},
		{`""`, nil},
		{"a,,b", []string{"a", "b"}},
		{"a, , b", []string{"a", "b"}},
	}
	for _, c := range cases {
		got := ParseEnvList(c.in)
		if len(got) == 0 && len(c.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseEnvList(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseEnvInt(t *testing.T) {
	cases := []struct {
		in   string
		def  int
		want int
	}{
		{`"15"`, 10, 15},
		{"20", 10, 20},
		{" 25 ", 10, 25},
		{"", 10, 10},
		{"abc", 10, 10},
		{`"abc"`, 7, 7},
	}
	for _, c := range cases {
		if got := ParseEnvInt(c.in, c.def); got != c.want {
			t.Errorf("ParseEnvInt(%q, %d) = %d, want %d", c.in, c.def, got, c.want)
		}
	}
}
