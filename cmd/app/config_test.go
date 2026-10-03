package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jfk9w-go/confi"

	"github.com/ar2r/ledger-fox/internal/jobs/lkdr"
)

func testSchema(t *testing.T) *confi.Schema {
	t.Helper()

	schema, err := confi.GenerateSchema(Config{})
	if err != nil {
		t.Fatal(err)
	}

	return schema
}

func TestWantsHelp(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}, {"--json", "--help"}} {
		if !wantsHelp(args) {
			t.Fatalf("expected help request for %v", args)
		}
	}

	for _, args := range [][]string{{}, {"--json"}, {"--help=1"}} {
		if wantsHelp(args) {
			t.Fatalf("unexpected help request for %v", args)
		}
	}
}

func TestKeyTreeKnownPaths(t *testing.T) {
	tree := buildKeyTree(testSchema(t))

	known := []string{
		"json", "debug", "log", "stdin", "stdin.enabled", "stdin.user",
		"lkdr", "lkdr.enabled", "lkdr.apiUrl", "lkdr.batchSize",
		"lkdr.maxRequests", "lkdr.timeout", "lkdr.database",
		"lkdr.users", "captcha", "captcha.rucaptchaKey",
		// Словарь пользователей принимает любой ключ ниже по пути.
		"lkdr.users.default.0.phone",
	}

	for _, path := range known {
		if !tree.known(path) {
			t.Errorf("path %q must be known", path)
		}
	}

	unknown := []string{
		"stdn.enabled", "lkdr.enable", "lkdr.batchsize", "lkdr.apiurl",
		"captcha.rucaptakey", "stdin.user.all.extra",
	}

	for _, path := range unknown {
		if tree.known(path) {
			t.Errorf("path %q must be unknown", path)
		}
	}
}

func TestCheckUnknownKeysFlags(t *testing.T) {
	schema := testSchema(t)

	if err := checkUnknownKeys(schema, []string{"--lkdr.batchSize=10", "--stdin.user=all", "--json", "--debug"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err := checkUnknownKeys(schema, []string{"--lkdr.batchsize=10"})
	if err == nil || !strings.Contains(err.Error(), "lkdr.batchsize") {
		t.Fatalf("expected unknown flag error, got %v", err)
	}
}

func TestCheckUnknownKeysFile(t *testing.T) {
	schema := testSchema(t)

	valid := `{"stdin": {"enabled": true, "user": "a"}, "lkdr": {"enabled": true, "users": {"a": [{"phone": "79001234567"}]}}}`
	path := writeTemp(t, "config.json", valid)
	if err := checkUnknownKeys(schema, []string{"--config.file=" + path}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	unknown := `{"stdin": {"enabled": true, "enable": true}}`
	path = writeTemp(t, "config.json", unknown)
	err := checkUnknownKeys(schema, []string{"--config.file=" + path})
	if err == nil || !strings.Contains(err.Error(), "stdin.enable") {
		t.Fatalf("expected unknown key error, got %v", err)
	}
}

func TestCheckUnknownKeysFileWithoutExtension(t *testing.T) {
	schema := testSchema(t)

	path := writeTemp(t, "config", `{"stdin": {"enabled": true}}`)
	err := checkUnknownKeys(schema, []string{"--config.file=" + path})
	if err == nil || !strings.Contains(err.Error(), "расширение") {
		t.Fatalf("expected extension error, got %v", err)
	}
}

func TestValidatePhones(t *testing.T) {
	newCfg := func(phones ...string) *Config {
		cfg := &Config{LKDR: &LKDRConfig{Enabled: true}}
		cfg.LKDR.Users = map[string][]lkdr.Credential{}
		for _, phone := range phones {
			cfg.LKDR.Users["a"] = append(cfg.LKDR.Users["a"], lkdr.Credential{Phone: phone})
		}

		return cfg
	}

	if err := validatePhones(newCfg("79001234567")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// LKDR выключен — телефоны не проверяются.
	disabled := newCfg("+79001234567")
	disabled.LKDR.Enabled = false
	if err := validatePhones(disabled); err != nil {
		t.Fatalf("unexpected error for disabled lkdr: %v", err)
	}

	for _, phone := range []string{"+79001234567", "89001234567", "7900123456", "790012345678", "домашний"} {
		err := validatePhones(newCfg(phone))
		if err == nil || !strings.Contains(err.Error(), phone) {
			t.Fatalf("expected format error for phone %q, got %v", phone, err)
		}
	}
}

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}
