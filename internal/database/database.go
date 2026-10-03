package database

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"reflect"
	"strings"

	"github.com/jfk9w-go/based"
	"github.com/pkg/errors"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

var namingStrategy = schema.NamingStrategy{
	IdentifierMaxLength: 64,
}

type Params struct {
	Clock    based.Clock  `validate:"required"`
	Logger   *slog.Logger `validate:"required"`
	Config   Config       `validate:"required"`
	Entities []any        `validate:"required"`
}

type DB struct {
	*gorm.DB
}

// withSQLitePragmas дописывает в DSN параметры соединения, если пользователь
// их не задал: WAL-журнал и busy_timeout убирают SQLITE_BUSY при параллельной
// записи разных пользователей в одну базу.
func withSQLitePragmas(dsn string) string {
	base, query, _ := strings.Cut(dsn, "?")
	values, err := url.ParseQuery(query)
	if err != nil {
		return dsn
	}

	defaults := map[string]string{
		"_journal_mode": "WAL",
		"_synchronous":  "NORMAL",
		"_busy_timeout": "5000",
	}

	changed := false
	for key, value := range defaults {
		if values.Get(key) == "" {
			values.Set(key, value)
			changed = true
		}
	}

	if !changed {
		return dsn
	}

	return base + "?" + values.Encode()
}

func Open(ctx context.Context, params Params) (DB, error) {
	if err := based.Validate(params); err != nil {
		return DB{}, err
	}

	db, err := gorm.Open(sqlite.Open(withSQLitePragmas(params.Config.DSN)), &gorm.Config{
		NowFunc: params.Clock.Now,
		Logger: slogLogger{
			logger: params.Logger,
			level:  logger.Warn,
		},
		FullSaveAssociations: true,
		NamingStrategy:       namingStrategy,
	})

	if err != nil {
		return DB{}, errors.Wrap(err, "open database")
	}

	if err := db.WithContext(ctx).AutoMigrate(params.Entities...); err != nil {
		return DB{}, errors.Wrap(err, "migrate database tables")
	}

	if params.Logger.Enabled(ctx, slog.LevelDebug) {
		db = db.Debug()
	}

	return DB{DB: db}, nil
}

func (db DB) WithContext(ctx context.Context) DB {
	db.DB = db.DB.WithContext(ctx)
	return db
}

func (db DB) Transaction(fn func(tx DB) error) error {
	return db.DB.Transaction(func(tx *gorm.DB) error {
		return fn(DB{DB: tx})
	})
}

func (db DB) Upsert(value any) *gorm.DB {
	return db.Clauses(extractUpsertClause(value)).Create(value)
}

func extractUpsertClause(entity any) clause.OnConflict {
	value := reflect.ValueOf(entity)
loop:
	for {
		switch value.Kind() {
		case reflect.Ptr:
			value = value.Elem()
		case reflect.Slice:
			// Пустой слайс не содержит типа элементов — до Upsert
			// добираются только с непустым (см. вызовы с len(...) > 0).
			if value.Len() == 0 {
				break loop
			}

			value = value.Index(0)
		default:
			break loop
		}
	}

	if value.Kind() != reflect.Struct {
		panic(fmt.Sprintf("expected %s, got %s", reflect.Struct, value.Kind()))
	}

	cnf := clause.OnConflict{
		UpdateAll: true,
	}

	for i := 0; i < value.NumField(); i++ {
		typeField := value.Type().Field(i)
		if typeField.Anonymous || !typeField.IsExported() {
			continue
		}

		tag := typeField.Tag.Get("gorm")
		if tag == "" {
			continue
		}

		settings := schema.ParseTagSetting(tag, ";")
		if _, ok := settings["PRIMARYKEY"]; !ok {
			continue
		}

		columnName := settings["COLUMN"]
		if columnName == "" {
			columnName = namingStrategy.ColumnName("", typeField.Name)
		}

		cnf.Columns = append(cnf.Columns, clause.Column{Name: columnName})
	}

	return cnf
}
