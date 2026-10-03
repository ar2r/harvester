package main

import (
	"context"
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

	Stdin *struct {
		Enabled bool   `yaml:"enabled,omitempty" doc:"Включение интерактивной командной строки."`
		User    string `yaml:"user,omitempty" doc:"ID пользователя, задания которого запускаются при старте без ввода."`
	} `yaml:"stdin,omitempty" doc:"Настройки управления через интерактивную командную строку."`

	LKDR *struct {
		lkdr.Config `yaml:",inline"`
		Enabled     bool `yaml:"enabled,omitempty" doc:"Включает загрузку данных из сервиса ФНС \"Мои чеки онлайн\"."`
	} `yaml:"lkdr,omitempty" doc:"Настройка загрузки данных из сервиса ФНС \"Мои чеки онлайн\"."`

	Captcha *captcha.Config `yaml:"captcha,omitempty" doc:"Настройки для решения капчи."`
}

func main() {
	var exitCode atomic.Int32
	defer func() {
		if code := int(exitCode.Load()); code != 0 {
			os.Exit(code)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg, _, err := confi.Get[Config](ctx, "hoarder")
	if err != nil {
		panic(err)
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
			panic(errors.Wrap(err, "create captcha solver"))
		}
	}

	jobs := new(jobs.Registry)

	var lkdrUsers []string
	if cfg := cfg.LKDR; pointer.Get(cfg).Enabled {
		job, err := lkdr.NewJob(ctx, lkdr.JobParams{
			Clock:         clock,
			Logger:        log,
			Config:        cfg.Config,
			CaptchaSolver: captchaSolver,
		})

		if err != nil {
			panic(errors.Wrapf(err, "create %s job", lkdr.JobID))
		}

		jobs.Register(job)

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
			panic(errors.Wrap(err, "create stdin trigger"))
		}

		triggers.Register(trigger)
	}

	triggers.Run(ctx, jobs)
	defer triggers.Close()

	if err := based.AwaitSignal(ctx, syscall.SIGINT, syscall.SIGTERM); err != nil && !errors.Is(err, context.Canceled) {
		panic(err)
	}
}
