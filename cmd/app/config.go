package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jfk9w-go/confi"
	"gopkg.in/yaml.v3"
)

const usage = `LedgerFox — сбор чеков ФНС «Мои чеки онлайн» в локальную SQLite-базу.

Использование:
  app --config.file=config.json [--stdin.user=<id|all>] [флаги]

Ключевые флаги (каждому ключу config.json соответствует флаг с тем же путём):
  --config.file=PATH     JSON-файл конфигурации
  --stdin.user=<id|all>  однократный запуск без интерактива: один пользователь
                         или all — все пользователи по алфавиту (для cron)
  --json                 весь вывод построчным JSON
  --debug                уровень логирования DEBUG
  --lkdr.maxRequests=N   максимум запросов выгрузки за запуск на загрузчик
  --lkdr.batchSize=N     размер страницы выгрузки чеков

Полный список ключей и значений: docs/configuration.md.
`

// wantsHelp сообщает, запрошена ли справка (--help или -h).
func wantsHelp(args []string) bool {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			return true
		}
	}

	return false
}

// configFilePaths возвращает все пути из повторяющихся флагов --config.file=
// в порядке следования (поздние файлы перекрывают ранние).
func configFilePaths(args []string) []string {
	var paths []string
	for _, arg := range args {
		if path, ok := strings.CutPrefix(arg, "--config.file="); ok && path != "" {
			paths = append(paths, path)
		}
	}

	return paths
}

// flagKeys выделяет из аргументов ключи конфигурационных флагов --key=value.
func flagKeys(args []string) map[string]struct{} {
	keys := make(map[string]struct{})
	for _, arg := range args {
		if !strings.HasPrefix(arg, "--") {
			continue
		}

		key, _, _ := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		if key != "" {
			keys[key] = struct{}{}
		}
	}

	return keys
}

// keyNode — узел дерева известных ключей конфигурации, построенного
// из схемы Config.
type keyNode struct {
	children map[string]*keyNode
	// anyKey — узел-словарь (например lkdr.users): любой ключ ниже по
	// пути считается допустимым.
	anyKey bool
}

func buildKeyTree(schema *confi.Schema) *keyNode {
	node := &keyNode{children: make(map[string]*keyNode)}
	if schema == nil {
		return node
	}

	for name, sub := range schema.Properties {
		node.children[name] = buildKeyTree(&sub)
	}

	if _, ok := schema.AdditionalProperties.(*confi.Schema); ok {
		node.anyKey = true
	}

	return node
}

func (n *keyNode) known(path string) bool {
	node := n
	for _, part := range strings.Split(path, ".") {
		if node.anyKey {
			return true
		}

		next, ok := node.children[part]
		if !ok {
			return false
		}

		node = next
	}

	return true
}

// checkUnknownKeys сверяет фактически заданные ключи (CLI-флаги и файлы
// конфигурации) со схемой: опечатка обязана падать на старте, а не
// молча игнорироваться при декодировании.
func checkUnknownKeys(schema *confi.Schema, args []string) error {
	if schema == nil {
		return nil
	}

	tree := buildKeyTree(schema)
	for key := range flagKeys(args) {
		// Служебные ключи самого confi, не входят в схему Config.
		if key == "config.file" || key == "config.stdin" {
			continue
		}

		if !tree.known(key) {
			return fmt.Errorf("неизвестный флаг --%s: проверьте написание (полный список ключей — docs/configuration.md, справка — --help)", key)
		}
	}

	for _, path := range configFilePaths(args) {
		if err := checkFileKeys(schema, path); err != nil {
			return err
		}
	}

	return nil
}

func checkFileKeys(schema *confi.Schema, path string) error {
	ext := strings.TrimPrefix(filepath.Ext(path), ".")
	var unmarshal func(data []byte, value any) error
	switch ext {
	case "json":
		unmarshal = json.Unmarshal
	case "yaml", "yml":
		unmarshal = yaml.Unmarshal
	default:
		// confi молча пропускает файл без известного расширения —
		// приложение стартовало бы с пустым конфигом.
		return fmt.Errorf("--config.file=%s: неизвестное расширение (ожидается .json)", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var value any
	if err := unmarshal(data, &value); err != nil {
		return err
	}

	return checkValueKeys(schema, value, "")
}

func checkValueKeys(schema *confi.Schema, value any, path string) error {
	switch value := value.(type) {
	case map[string]any:
		if additional, ok := schema.AdditionalProperties.(*confi.Schema); ok {
			for key, sub := range value {
				if err := checkValueKeys(additional, sub, keyPath(path, key)); err != nil {
					return err
				}
			}

			return nil
		}

		if schema.Properties == nil {
			return nil
		}

		for key, sub := range value {
			property, ok := schema.Properties[key]
			if !ok {
				return fmt.Errorf("неизвестный ключ %s в конфигурации: проверьте написание (полный список ключей — docs/configuration.md, справка — --help)", keyPath(path, key))
			}

			if err := checkValueKeys(&property, sub, keyPath(path, key)); err != nil {
				return err
			}
		}

	case []any:
		if schema.Items != nil {
			for _, sub := range value {
				if err := checkValueKeys(schema.Items, sub, path); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func keyPath(path, key string) string {
	if path == "" {
		return key
	}

	return path + "." + key
}

var phonePattern = regexp.MustCompile(`^7\d{10}$`)

// validatePhones проверяет формат телефонов на старте: ошибка формата
// иначе всплывает только в рантайме от API с невнятным текстом.
func validatePhones(cfg *Config) error {
	if cfg.LKDR == nil || !cfg.LKDR.Enabled {
		return nil
	}

	for user, credentials := range cfg.LKDR.Users {
		for i, credential := range credentials {
			if !phonePattern.MatchString(credential.Phone) {
				return fmt.Errorf("lkdr.users.%s[%d].phone = %q: ожидается 11 цифр, начиная с 7 (например 79001234567)", user, i, credential.Phone)
			}
		}
	}

	return nil
}
