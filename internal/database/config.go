package database

type Config struct {
	DSN string `yaml:"dsn" doc:"Путь к файлу базы данных SQLite."`
}
