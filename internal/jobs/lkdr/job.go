package lkdr

import (
	"context"
	"hash/fnv"
	"log/slog"
	"math/rand"
	"net/http"
	"time"

	"github.com/jfk9w-go/based"
	"github.com/jfk9w-go/lkdr-api"
	"github.com/pkg/errors"
	"go.uber.org/multierr"

	"github.com/ar2r/harvester/internal/captcha"
	"github.com/ar2r/harvester/internal/common"
	"github.com/ar2r/harvester/internal/database"
	"github.com/ar2r/harvester/internal/jobs"
	. "github.com/ar2r/harvester/internal/jobs/lkdr/internal/entities"
	"github.com/ar2r/harvester/internal/jobs/lkdr/internal/loaders"
	"github.com/ar2r/harvester/internal/logs"
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
	users     map[string]map[string]Client
	batchSize int
	// maxRequests — ограничитель запросов выгрузки за запуск на загрузчик
	// (lkdr.maxRequests); 0 — без ограничения.
	maxRequests   int
	captchaSolver captcha.TokenProvider
	db            database.DB
}

func NewJob(ctx context.Context, params JobParams) (*Job, error) {
	if err := based.Validate(params); err != nil {
		return nil, err
	}

	if params.ClientFactory == nil {
		params.ClientFactory = defaultClientFactory
	}

	if params.Config.MaxRequests < 0 {
		return nil, errors.Errorf("lkdr.maxRequests %d: must not be negative", params.Config.MaxRequests)
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
	for user, credentials := range params.Config.Users {
		phones := make(map[string]Client)
		users[user] = phones
		for _, credential := range credentials {
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
		users:         users,
		batchSize:     params.Config.BatchSize,
		maxRequests:   params.Config.MaxRequests,
		captchaSolver: params.CaptchaSolver,
		db:            db,
	}, nil
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
			Phone:       phone,
			BatchSize:   j.batchSize,
			MaxRequests: j.maxRequests,
		},
		loaders.FiscalData{Phone: phone, BatchSize: j.batchSize, MaxRequests: j.maxRequests},
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
