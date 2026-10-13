package config_test

import (
	"strings"
	"testing"

	"github.com/SnWalker/kowa/internal/platform/config"
)

func setRunnerEnv(t *testing.T, values map[string]string) {
	t.Helper()
	keys := []string{
		"KOWA_RUNNER_SERVER_URL", "KOWA_RUNNER_RUNTIME_ID", "KOWA_RUNNER_RUNTIME_TOKEN",
		"KOWA_RUNNER_WORK_ROOT", "KOWA_RUNNER_PROVIDER", "KOWA_RUNNER_PROVIDER_VERSION",
		"KOWA_RUNNER_PROVIDER_MODEL", "KOWA_RUNNER_SCRIPT", "KOWA_RUNNER_CAPABILITIES",
		"KOWA_RUNNER_GITHUB_USER_ID", "KOWA_RUNNER_LEASE_SECONDS", "KOWA_RUNNER_ENV_ALLOWLIST",
	}
	for _, key := range keys {
		t.Setenv(key, "")
	}
	for key, value := range values {
		t.Setenv(key, value)
	}
}

func validRunnerEnv() map[string]string {
	return map[string]string{
		"KOWA_RUNNER_SERVER_URL":     "http://127.0.0.1:8080",
		"KOWA_RUNNER_RUNTIME_ID":     "runtime-1",
		"KOWA_RUNNER_RUNTIME_TOKEN":  "0123456789abcdef0123456789abcdef",
		"KOWA_RUNNER_WORK_ROOT":      "/tmp/kowa-runner-tests",
		"KOWA_RUNNER_PROVIDER":       "scripted",
		"KOWA_RUNNER_CAPABILITIES":   "plan.create@1,code.change@2",
		"KOWA_RUNNER_GITHUB_USER_ID": "135722937",
	}
}

func TestRunnerConfigLoadsValidEnvironment(t *testing.T) {
	setRunnerEnv(t, validRunnerEnv())
	runner, err := config.NewRunner()
	if err != nil {
		t.Fatal(err)
	}
	if runner.LeaseSeconds != 1800 || len(runner.Capabilities) != 2 ||
		runner.Capabilities[0].ID != "plan.create" || runner.Capabilities[0].Version != 1 {
		t.Fatalf("runner = %+v", runner)
	}
	if strings.Join(runner.EnvAllowlist, ",") != "PATH,HOME,LANG,LC_ALL,TERM" {
		t.Fatalf("default allowlist = %v", runner.EnvAllowlist)
	}
}

func TestRunnerConfigRejectsInvalidValues(t *testing.T) {
	cases := map[string]map[string]string{
		"missing server url":   {"KOWA_RUNNER_SERVER_URL": ""},
		"non http scheme":      {"KOWA_RUNNER_SERVER_URL": "ftp://example.com"},
		"short token":          {"KOWA_RUNNER_RUNTIME_TOKEN": "short"},
		"relative work root":   {"KOWA_RUNNER_WORK_ROOT": "relative/path"},
		"unknown provider":     {"KOWA_RUNNER_PROVIDER": "gpt"},
		"bad capability":       {"KOWA_RUNNER_CAPABILITIES": "plan.create"},
		"duplicate capability": {"KOWA_RUNNER_CAPABILITIES": "plan.create@1,plan.create@1"},
		"bad github id":        {"KOWA_RUNNER_GITHUB_USER_ID": "user"},
		"lease out of bounds":  {"KOWA_RUNNER_LEASE_SECONDS": "5"},
		"secret allowlist":     {"KOWA_RUNNER_ENV_ALLOWLIST": "PATH,GITHUB_TOKEN"},
		"ssh allowlist":        {"KOWA_RUNNER_ENV_ALLOWLIST": "PATH,SSH_AUTH_SOCK"},
		"bad allowlist name":   {"KOWA_RUNNER_ENV_ALLOWLIST": "PATH,path"},
	}
	for name, overrides := range cases {
		t.Run(name, func(t *testing.T) {
			values := validRunnerEnv()
			for key, value := range overrides {
				values[key] = value
			}
			setRunnerEnv(t, values)
			if _, err := config.NewRunner(); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}
