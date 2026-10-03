package stdin

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jfk9w-go/based"
	"go.uber.org/multierr"

	"github.com/ar2r/ledger-fox/internal/logs"
	"github.com/ar2r/ledger-fox/internal/triggers"
)

const TriggerID = "stdin"

// AllUsers — специальное значение --stdin.user: по очереди выполнить
// задачи для каждого пользователя из конфигурации.
const AllUsers = "all"

type TriggerParams struct {
	Clock based.Clock `validate:"required"`
	User  string
	// Users — ID всех пользователей из конфигурации; используется,
	// когда User == AllUsers.
	Users []string
	// JSON переключает вывод триггера с человекочитаемого текста
	// (приглашения, ✔/✘) на построчный JSON.
	JSON   bool
	Reader io.Reader
	Writer io.Writer
	Exit   func(code int) `validate:"required"`
}

type Trigger struct {
	clock based.Clock
	user  string
	users []string
	json  bool
	in    io.Reader
	out   io.Writer
	exit  func(code int)
	// lines наполняется единственной горутиной-читателем (Run); канал
	// закрывается по EOF. Один сканер на всё время жизни триггера, чтобы
	// повторные ask не теряли строки, считанные в буфер с опережением.
	lines chan string
}

func NewTrigger(params TriggerParams) (*Trigger, error) {
	if err := based.Validate(params); err != nil {
		return nil, err
	}

	if params.Reader == nil {
		params.Reader = os.Stdin
	}

	if params.Writer == nil {
		params.Writer = os.Stdout
	}

	return &Trigger{
		clock: params.Clock,
		user:  params.User,
		users: params.Users,
		json:  params.JSON,
		in:    params.Reader,
		out:   params.Writer,
		exit:  params.Exit,
	}, nil
}

func (t *Trigger) ID() string {
	return TriggerID
}

func (t *Trigger) Run(ctx triggers.Context, job triggers.Jobs) {
	t.lines = make(chan string)
	go func() {
		defer close(t.lines)
		scanner := bufio.NewScanner(t.in)
		for scanner.Scan() {
			t.lines <- scanner.Text()
		}
	}()

	if t.user == AllUsers && len(t.users) > 0 {
		code := 0
		for _, user := range t.users {
			// Каждый пользователь выполняется по очереди; ошибка одного
			// не прерывает остальных, но портит итоговый код возврата.
			if c := t.run(ctx.As(user), job, user); c != 0 {
				code = c
			}
		}

		t.exit(code)
		return
	}

	if t.user != "" {
		t.exit(t.run(ctx.As(t.user), job, t.user))
		return
	}

	for {
		userID, err := t.ask(ctx, "Enter user: ")
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				ctx.Error("failed to get user", logs.Error(err))
			}

			// stdin закрыт (EOF) — процесс завершается сам, а не виснет
			// в ожидании сигнала.
			t.exit(0)
			return
		}

		_ = t.run(ctx.As(userID), job, userID)
	}
}

func (t *Trigger) run(ctx triggers.Context, job triggers.Jobs, userID string) (code int) {
	results := job.Run(ctx.Job().WithAskFn(t.ask), t.clock.Now(), userID, nil)

	if t.json {
		for _, result := range results {
			if result.Error == nil {
				t.emit(ctx, map[string]any{"event": "job", "status": "ok", "user": userID, "job": result.JobID})
				continue
			}

			code = 1
			for _, err := range multierr.Errors(result.Error) {
				t.emit(ctx, map[string]any{
					"event":  "job",
					"status": "error",
					"user":   userID,
					"job":    result.JobID,
					"error":  err.Error(),
				})
			}
		}

		return
	}

	var reply strings.Builder
	for _, result := range results {
		if result.Error == nil {
			reply.WriteString(" ✔ ")
			reply.WriteString(userID)
			reply.WriteString("/")
			reply.WriteString(result.JobID)
			reply.WriteRune('\n')
		} else {
			code = 1
			for _, err := range multierr.Errors(result.Error) {
				reply.WriteString(" ✘ ")
				reply.WriteString(userID)
				reply.WriteString("/")
				reply.WriteString(result.JobID)
				reply.WriteString(": ")
				reply.WriteString(err.Error())
				reply.WriteRune('\n')
			}
		}
	}

	if _, err := fmt.Fprintln(t.out, reply.String()); err != nil {
		ctx.Error("failed to print result", logs.Error(err))
	}

	return
}

func (t *Trigger) ask(ctx context.Context, prompt string) (string, error) {
	if t.json {
		if err := json.NewEncoder(t.out).Encode(map[string]any{"event": "prompt", "message": prompt}); err != nil {
			return "", err
		}
	} else if _, err := fmt.Fprint(t.out, prompt); err != nil {
		return "", err
	}

	// Ожидание ввода прерывается отменой контекста (Ctrl+C / остановка
	// приложения): оставшаяся заблокированной горутина-читатель умирает
	// вместе с процессом.
	select {
	case line, ok := <-t.lines:
		if !ok {
			return "", errors.New("stdin closed")
		}

		return line, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (t *Trigger) emit(ctx triggers.Context, value map[string]any) {
	if err := json.NewEncoder(t.out).Encode(value); err != nil {
		ctx.Error("failed to print result", logs.Error(err))
	}
}
