package lkdr

import (
	"context"
	"hash/fnv"
	"log/slog"
	"math/rand"
	"net/http"
	"time"

	"github.com/AlekSi/pointer"
	"github.com/jfk9w-go/based"
	"github.com/jfk9w-go/lkdr-api"
	"github.com/pkg/errors"
	"go.uber.org/multierr"

	"github.com/jfk9w/hoarder/internal/captcha"
	"github.com/jfk9w/hoarder/internal/common"
	"github.com/jfk9w/hoarder/internal/database"
	"github.com/jfk9w/hoarder/internal/jobs"
	. "github.com/jfk9w/hoarder/internal/jobs/lkdr/internal/entities"
	"github.com/jfk9w/hoarder/internal/jobs/lkdr/internal/loaders"
	"github.com/jfk9w/hoarder/internal/logs"
)

const JobID = "lkdr"

type JobParams struct {
	Config        Config       `validate:"required"`
	Clock         based.Clock  `validate:"required"`
	Logger        *slog.Logger `validate:"required"`
	ClientFactory ClientFactory
	CaptchaSolver captcha.TokenProvider
}

type Job struct {
	users map[string]map[string]Client
	// firstSyncFrom — дата первой синхронизации на каждый телефон:
	// per-user firstSyncMonths, глобальный firstSyncFrom или nil
	// (тогда загрузчик берёт 12 месяцев по умолчанию).
	firstSyncFrom map[string]*lkdr.Date
	// firstSyncMonths — желаемая глубина истории на каждый телефон:
	// при инкрементальных запусках докачивает старые чеки, если
	// накопленная история короче окна (0 — не задана).
	firstSyncMonths map[string]int
	batchSize       int
	captchaSolver   captcha.TokenProvider
	db              database.DB
}

func NewJob(ctx context.Context, params JobParams) (*Job, error) {
	if err := based.Validate(params); err != nil {
		return nil, err
	}

	if params.ClientFactory == nil {
		params.ClientFactory = defaultClientFactory
	}

	firstSyncFrom, err := parseFirstSyncFrom(params.Config.FirstSyncFrom)
	if err != nil {
		return nil, err
	}

	db, err := database.Open(ctx, database.Params{
		Clock:    params.Clock,
		Logger:   params.Logger.With(logs.Database(JobID)),
		Config:   params.Config.Database,
		Entities: entities,
	})

	if err != nil {
		return nil, err
	}

	storage := &storage{db: db}

	var apiTransport http.RoundTripper
	if apiURL := params.Config.APIURL; apiURL != "" {
		apiTransport, err = NewRedirectTransport(apiURL)
		if err != nil {
			return nil, errors.Wrap(err, "create api transport")
		}
	}

	users := make(map[string]map[string]Client)
	firstSyncDates := make(map[string]*lkdr.Date)
	firstSyncMonths := make(map[string]int)
	for user, credentials := range params.Config.Users {
		phones := make(map[string]Client)
		users[user] = phones
		for _, credential := range credentials {
			syncFrom, depth, err := firstSyncFor(params.Clock.Now(), firstSyncFrom, params.Config.FirstSyncMonths, credential)
			if err != nil {
				return nil, errors.Wrapf(err, "user %s", user)
			}

			firstSyncDates[credential.Phone] = syncFrom
			firstSyncMonths[credential.Phone] = depth

			deviceID := credential.DeviceID
			if deviceID == "" {
				var err error
				deviceID, err = generateDeviceID(credential.UserAgent, credential.Phone)
				if err != nil {
					return nil, errors.Wrap(err, "generate device ID")
				}
			}

			client, err := params.ClientFactory(lkdr.ClientParams{
				Phone:        credential.Phone,
				Clock:        params.Clock,
				DeviceID:     deviceID,
				UserAgent:    credential.UserAgent,
				TokenStorage: storage,
				Transport:    apiTransport,
			})

			if err != nil {
				return nil, errors.Wrapf(err, "create client for %s/%s", user, credential.Phone)
			}

			phones[credential.Phone] = &boundClient{
				client:  client,
				timeout: params.Config.Timeout,
			}
		}
	}

	return &Job{
		users:           users,
		firstSyncFrom:   firstSyncDates,
		firstSyncMonths: firstSyncMonths,
		batchSize:       params.Config.BatchSize,
		captchaSolver:   params.CaptchaSolver,
		db:              db,
	}, nil
}

// firstSyncFor решает, с какой даты качать чеки при первой синхронизации
// телефона и какая минимальная глубина истории нужна на инкрементальных
// запусках: действует большая из глубин — lkdr.firstSyncMonths и настройка
// пользователя firstSyncMonths (чтобы разовая докачка вглубь не срезалась
// меньшей настройкой из config.json). Без глубин — глобальный firstSyncFrom;
// без него nil — загрузчик возьмёт последние 12 месяцев.
func firstSyncFor(now time.Time, globalFrom *lkdr.Date, globalMonths int, credential Credential) (*lkdr.Date, int, error) {
	if globalMonths < 0 {
		return nil, 0, errors.Errorf("lkdr.firstSyncMonths %d: must be positive", globalMonths)
	}

	if credential.FirstSyncMonths < 0 {
		return nil, 0, errors.Errorf("firstSyncMonths %d: must be positive", credential.FirstSyncMonths)
	}

	if months := max(globalMonths, credential.FirstSyncMonths); months > 0 {
		return pointer.To(lkdr.Date(now.AddDate(0, -months, 0))), months, nil
	}

	return globalFrom, 0, nil
}

func (j *Job) Info() jobs.Info {
	return jobs.Info{
		ID:          JobID,
		Description: `Загрузка данных из сервиса ФНС "Мои чеки онлайн"`,
	}
}

func (j *Job) Run(ctx jobs.Context, _ time.Time, userID string) (errs error) {
	phones := j.users[userID]
	if phones == nil {
		return jobs.ErrJobUnconfigured
	}

	ctx = ctx.ApplyAskFn(withAuthorizer(j.captchaSolver))
	for phone, client := range phones {
		ctx := ctx.With("phone", phone)
		err := j.executeLoaders(ctx, userID, phone, client)
		_ = multierr.AppendInto(&errs, err)
	}

	return
}

func (j *Job) executeLoaders(ctx jobs.Context, userID, phone string, client Client) (errs error) {
	if err := j.db.WithContext(ctx).
		Upsert(&User{Name: userID, Phone: phone}).
		Error; ctx.Error(&errs, err, "failed to create user in db") {
		return
	}

	var stack common.Stack[loaders.Interface]
	stack.Push(
		loaders.Receipts{
			Phone:          phone,
			BatchSize:      j.batchSize,
			FirstSyncFrom:  j.firstSyncFrom[phone],
			MinDepthMonths: j.firstSyncMonths[phone],
		},
		loaders.FiscalData{Phone: phone, BatchSize: j.batchSize},
	)

	for {
		loader, ok := stack.Pop()
		if !ok {
			break
		}

		ctx := ctx.With("entity", loader.TableName())
		loaders, err := loader.Load(ctx, client, j.db)
		if !multierr.AppendInto(&errs, err) {
			stack.Push(loaders...)
		}
	}

	return
}

func parseFirstSyncFrom(value string) (*lkdr.Date, error) {
	if value == "" {
		return nil, nil
	}

	date, err := time.Parse("2006-01-02", value)
	if err != nil {
		return nil, errors.Wrapf(err, "parse firstSyncFrom %q: expected YYYY-MM-DD", value)
	}

	return pointer.To(lkdr.Date(date)), nil
}

func generateDeviceID(userAgent, phone string) (string, error) {
	hash := fnv.New64()
	if _, err := hash.Write([]byte(userAgent)); err != nil {
		return "", errors.Wrap(err, "hash user agent")
	}

	if _, err := hash.Write([]byte(phone)); err != nil {
		return "", errors.Wrap(err, "hash phone")
	}

	source := rand.NewSource(int64(hash.Sum64()))

	const symbols = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	var deviceID []byte
	for i := 0; i < 21; i++ {
		deviceID = append(deviceID, symbols[source.Int63()%int64(len(symbols))])
	}

	return string(deviceID), nil
}
