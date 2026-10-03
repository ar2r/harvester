package main

// E2E-тесты запускают собранный бинарник app как чёрный ящик против
// мок-сервиса API ФНС (internal/mocklkdr): проверяется конфигурация,
// авторестарт по --stdin.user, порядок пользователей в all, форматы
// вывода (текст/JSON) и коды возврата.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/ar2r/ledger-fox/internal/mocklkdr"
)

var appBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ledgerfox-e2e")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	defer os.RemoveAll(dir)

	appBin = filepath.Join(dir, "app")
	build := exec.Command("go", "build", "-o", appBin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build app: %v\n%s", err, out)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

func runApp(t *testing.T, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, appBin, args...)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	// Чистое окружение: переменные HOARDER_* не должны влиять на тест,
	// TZ=UTC делает времена токенов детерминированными.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "TZ=UTC"}

	err := cmd.Run()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run app: %v\nstdout: %s\nstderr: %s", err, out.String(), errOut.String())
		}

		code = exitErr.ExitCode()
	}

	return out.String(), errOut.String(), code
}

// writeConfig создаёт конфиг с включёнными lkdr и stdin и возвращает
// путь к нему. Пользователи передаются как map<имя, телефон>.
func writeConfig(t *testing.T, apiURL, dsn string, users map[string]string) string {
	t.Helper()

	configUsers := make(map[string][]e2eCredential, len(users))
	for name, phone := range users {
		configUsers[name] = []e2eCredential{{Phone: phone, UserAgent: "e2e-agent"}}
	}

	config := map[string]any{
		"stdin": map[string]any{"enabled": true},
		"lkdr": map[string]any{
			"enabled":  true,
			"apiUrl":   apiURL,
			"database": map[string]any{"dsn": dsn},
			"users":    configUsers,
		},
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

type e2eCredential struct {
	Phone     string `json:"phone"`
	UserAgent string `json:"userAgent"`
}

// seedTokens создаёт БД с минимальной схемой (users, tokens) и валидными
// токенами для указанных телефонов, чтобы запуск не требовал
// SMS-авторизации. Колонки соответствуют миграции GORM (entities.User,
// entities.Tokens); AutoMigrate приложения дополняет схему без конфликтов.
func seedTokens(t *testing.T, dsn string, phones ...string) {
	t.Helper()

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatal(err)
	}

	defer db.Close()

	statements := []string{
		`CREATE TABLE IF NOT EXISTS users (phone text PRIMARY KEY, name text)`,
		`CREATE TABLE IF NOT EXISTS tokens (
			user_phone text PRIMARY KEY,
			refresh_token text,
			refresh_token_expires_in datetime,
			token text,
			token_expire_in datetime)`,
	}

	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	// UTC и запас по срокам: DateTimeTZ сериализуется wall-clock временем
	// без зоны (см. integration_test.go).
	now := time.Now().UTC()
	for _, phone := range phones {
		_, err := db.Exec(
			`INSERT OR REPLACE INTO tokens
				(user_phone, token, token_expire_in, refresh_token, refresh_token_expires_in)
				VALUES (?, ?, ?, ?, ?)`,
			phone,
			"seeded-token",
			now.Add(7*24*time.Hour),
			"seed-refresh-token",
			now.Add(30*24*time.Hour),
		)

		if err != nil {
			t.Fatal(err)
		}

		if _, err := db.Exec(`INSERT OR REPLACE INTO users (phone, name) VALUES (?, ?)`, phone, phone); err != nil {
			t.Fatal(err)
		}
	}
}

func TestE2EAutostartRunSucceeds(t *testing.T) {
	server := httptest.NewServer(mocklkdr.New().Handler())
	defer server.Close()

	dsn := filepath.Join(t.TempDir(), "lkdr.db")
	seedTokens(t, dsn, "79000000000")
	config := writeConfig(t, server.URL, dsn, map[string]string{"a": "79000000000"})

	stdout, _, code := runApp(t, "--config.file="+config, "--stdin.user=a")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d\nstdout: %s", code, stdout)
	}

	if !strings.Contains(stdout, "✔ a/lkdr") {
		t.Fatalf("expected success marker in output, got %q", stdout)
	}
}

func TestE2EAllUsersRunInSortedOrder(t *testing.T) {
	server := httptest.NewServer(mocklkdr.New().Handler())
	defer server.Close()

	// Порядок в map не гарантирован — приложение должно отсортировать.
	dsn := filepath.Join(t.TempDir(), "lkdr.db")
	seedTokens(t, dsn, "79000000000", "79000000001")
	config := writeConfig(t, server.URL, dsn, map[string]string{
		"beta":  "79000000001",
		"alpha": "79000000000",
	})

	stdout, _, code := runApp(t, "--config.file="+config, "--stdin.user=all")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d\nstdout: %s", code, stdout)
	}

	alpha := strings.Index(stdout, "✔ alpha/lkdr")
	beta := strings.Index(stdout, "✔ beta/lkdr")
	if alpha < 0 || beta < 0 || alpha > beta {
		t.Fatalf("expected alpha before beta in output, got %q", stdout)
	}
}

func TestE2EJSONOutput(t *testing.T) {
	server := httptest.NewServer(mocklkdr.New().Handler())
	defer server.Close()

	dsn := filepath.Join(t.TempDir(), "lkdr.db")
	seedTokens(t, dsn, "79000000000")
	config := writeConfig(t, server.URL, dsn, map[string]string{"a": "79000000000"})

	stdout, stderr, code := runApp(t, "--config.file="+config, "--stdin.user=a", "--json")
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d\nstdout: %s", code, stdout)
	}

	// Весь stdout — построчный JSON с итогом задачи.
	var last map[string]any
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if line == "" {
			continue
		}

		var value map[string]any
		if err := json.Unmarshal([]byte(line), &value); err != nil {
			t.Fatalf("stdout line is not json (%q): %v", line, err)
		}

		last = value
	}

	if last == nil || last["event"] != "job" || last["status"] != "ok" ||
		last["user"] != "a" || last["job"] != "lkdr" {
		t.Fatalf("unexpected final json event: %v", last)
	}

	// --json переключает и логи в NDJSON.
	var sawJSONLog bool
	for _, line := range strings.Split(strings.TrimSpace(stderr), "\n") {
		if line == "" {
			continue
		}

		var value map[string]any
		if err := json.Unmarshal([]byte(line), &value); err != nil {
			t.Fatalf("stderr line is not json (%q): %v", line, err)
		}

		if _, ok := value["level"]; ok {
			sawJSONLog = true
		}
	}

	if !sawJSONLog {
		t.Fatalf("expected json log records on stderr, got %q", stderr)
	}
}

func TestE2EFailingJobSetsExitCode(t *testing.T) {
	// Недоступный API: любое обращение к сервису падает, задача
	// завершается ошибкой, процесс возвращает 1.
	server := httptest.NewServer(mocklkdr.New().Handler())
	apiURL := server.URL
	server.Close()

	dsn := filepath.Join(t.TempDir(), "lkdr.db")
	seedTokens(t, dsn, "79000000000")
	config := writeConfig(t, apiURL, dsn, map[string]string{"a": "79000000000"})

	stdout, _, code := runApp(t, "--config.file="+config, "--stdin.user=a")
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d\nstdout: %s", code, stdout)
	}

	if !strings.Contains(stdout, "✘ a/lkdr") {
		t.Fatalf("expected failure marker in output, got %q", stdout)
	}
}
