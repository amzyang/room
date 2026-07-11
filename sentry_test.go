package main

import "testing"

func TestResolveSentryDSN(t *testing.T) {
	const builtin = "https://builtin@example.ingest.sentry.io/1"

	tests := []struct {
		name      string
		flagValue string
		flagSet   bool
		envValue  string
		envSet    bool
		builtin   string
		want      string
	}{
		{
			name:      "flag 优先于 env 与内置",
			flagValue: "https://flag@example.ingest.sentry.io/2",
			flagSet:   true,
			envValue:  "https://env@example.ingest.sentry.io/3",
			envSet:    true,
			builtin:   builtin,
			want:      "https://flag@example.ingest.sentry.io/2",
		},
		{
			name:     "flag 显式设空则禁用",
			flagSet:  true,
			envValue: "https://env@example.ingest.sentry.io/3",
			envSet:   true,
			builtin:  builtin,
			want:     "",
		},
		{
			name:     "env 优先于内置",
			envValue: "https://env@example.ingest.sentry.io/3",
			envSet:   true,
			builtin:  builtin,
			want:     "https://env@example.ingest.sentry.io/3",
		},
		{
			name:    "env 显式设空则禁用",
			envSet:  true,
			builtin: builtin,
			want:    "",
		},
		{
			name:    "均未设置时用内置",
			builtin: builtin,
			want:    builtin,
		},
		{
			name: "均未设置且无内置则禁用",
			want: "",
		},
		{
			name:      "flag 值清洗引号与空白",
			flagValue: ` "https://flag@example.ingest.sentry.io/2" `,
			flagSet:   true,
			want:      "https://flag@example.ingest.sentry.io/2",
		},
		{
			name:    "内置值清洗引号与空白",
			builtin: ` "https://builtin@example.ingest.sentry.io/1" `,
			want:    "https://builtin@example.ingest.sentry.io/1",
		},
		{
			name:     "env 清洗后为空视为禁用",
			envValue: `" "`,
			envSet:   true,
			builtin:  builtin,
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveSentryDSN(tt.flagValue, tt.flagSet, tt.envValue, tt.envSet, tt.builtin)
			if got != tt.want {
				t.Errorf("resolveSentryDSN() = %q, want %q", got, tt.want)
			}
		})
	}
}
