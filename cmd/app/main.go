package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"sync/atomic"
	"syscall"

	"github.com/AlekSi/pointer"
	"github.com/jfk9w-go/based"
	"github.com/jfk9w-go/confi"
	"github.com/pkg/errors"

	"github.com/ar2r/ledger-fox/internal/captcha"
	"github.com/ar2r/ledger-fox/internal/jobs"
	"github.com/ar2r/ledger-fox/internal/jobs/lkdr"
	"github.com/ar2r/ledger-fox/internal/logs"
	"github.com/ar2r/ledger-fox/internal/triggers"
	"github.com/ar2r/ledger-fox/internal/triggers/stdin"
)

type Config struct {
	// JSON переключает весь вывод (логи и сообщения триггеров) в формат JSON;
	// по умолчанию — текстовый режим для человека.
	JSON bool `yaml:"json,omitempty" doc:"Выводить всю информацию (логи и сообщения триггеров) в формате JSON. По умолчанию текстовый режим."`

	// Debug поднимает уровень логирования до DEBUG (детальный вывод);
	// по умолчанию — уровень INFO.
	Debug bool `yaml:"debug,omitempty" doc:"Включить детальный (DEBUG) уровень логирования. По умолчанию INFO."`

	Log logs.Config `yaml:"log,omitempty" doc:"Настройки логирования для библиотеки slog."`

	Stdin *StdinConfig `yaml:"stdin,omitempty" doc:"Настройки управления через интерактивную командную строку."`

	LKDR *LKDRConfig `yaml:"lkdr,omitempty" doc:"Настройка загрузки данных из сервиса ФНС \"Мои чеки онлайн\"."`

	Captcha *captcha.Config `yaml:"captcha,omitempty" doc:"Настройки для решения капчи."`
}

type StdinConfig struct {
	Enabled bool   `yaml:"enabled,omitempty" doc:"Включение интерактивной командной строки."`
	User    string `yaml:"user,omitempty" doc:"ID пользователя, задания которого запускаются при старте без ввода."`
}

type LKDRConfig struct {
	lkdr.Config `yaml:",inline"`
	Enabled     bool `yaml:"enabled,omitempty" doc:"Включает загрузку данных из сервиса ФНС \"Мои чеки онлайн\"."`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ошибка запуска:", err)
		os.Exit(1)
	}
}

func run() error {
	if wantsHelp(os.Args[1:]) {
		fmt.Print(usage)
		return nil
	}

	var exitCode atomic.Int32
	defer func() {
		if code := int(exitCode.Load()); code != 0 {
			os.Exit(code)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg, schema, err := confi.Get[Config](ctx, "hoarder")
	if err != nil {
		return err
	}

	if err := checkUnknownKeys(schema, os.Args[1:]); err != nil {
		return err
	}

	if err := validatePhones(cfg); err != nil {
		return err
	}

	jsonOutput := cfg.JSON
	if jsonOutput {
		cfg.Log.Encoding = logs.JSON
	}

	if cfg.Debug {
		cfg.Log.Level = slog.LevelDebug
	}

	log := logs.Get(cfg.Log)
	defer log.Info("shutdown")

	clock := based.StandardClock

	var captchaSolver captcha.TokenProvider
	if cfg := cfg.Captcha; cfg != nil {
		captchaSolver, err = captcha.NewTokenProvider(cfg, clock)
		if err != nil {
			return errors.Wrap(err, "create captcha solver")
		}
	}

	jobs := new(jobs.Registry)
	jobsEnabled := false

	var lkdrUsers []string
	if cfg := cfg.LKDR; pointer.Get(cfg).Enabled {
		job, err := lkdr.NewJob(ctx, lkdr.JobParams{
			Clock:         clock,
			Logger:        log,
			Config:        cfg.Config,
			CaptchaSolver: captchaSolver,
		})

		if err != nil {
			return errors.Wrapf(err, "create %s job", lkdr.JobID)
		}

		jobs.Register(job)
		jobsEnabled = true

		for user := range cfg.Config.Users {
			lkdrUsers = append(lkdrUsers, user)
		}

		// Предсказуемый порядок запуска для --stdin.user=all.
		sort.Strings(lkdrUsers)
	}

	triggers := triggers.NewRegistry(log)

	if cfg := cfg.Stdin; pointer.Get(cfg).Enabled {
		trigger, err := stdin.NewTrigger(stdin.TriggerParams{
			Clock: clock,
			User:  cfg.User,
			Users: lkdrUsers,
			JSON:  jsonOutput,
			Exit: func(code int) {
				exitCode.Store(int32(code))
				cancel()
			},
		})

		if err != nil {
			return errors.Wrap(err, "create stdin trigger")
		}

		triggers.Register(trigger)
	}

	// Fail-fast: ни одной включённой подсистемы означает опечатку в ключе
	// enabled или пустой конфиг — приложение молча висело бы без работы.
	if !jobsEnabled {
		return errors.New("не найдено ни одной включённой задачи: включите lkdr.enabled=true в config.json (см. docs/getting-started.md)")
	}

	if len(triggers.Info()) == 0 {
		return errors.New("не найдено ни одного включённого триггера: включите stdin.enabled=true в config.json (см. docs/getting-started.md)")
	}

	triggers.Run(ctx, jobs)
	defer triggers.Close()

	if err := based.AwaitSignal(ctx, syscall.SIGINT, syscall.SIGTERM); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}

	return nil
}
