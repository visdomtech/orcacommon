package postgres

import (
	"log/slog"
	"net/url"
	"strconv"
	"strings"
)

// DBConfig holds the database connection parameters.
// Environment variables are read with the "DB_" prefix (e.g. DB_HOST, DB_PORT).
type DBConfig struct {
	Host                string `env:"HOST"                  envDefault:"localhost"`
	Port                int    `env:"PORT"                  envDefault:"5432"`
	User                string `env:"USER"`
	Password            string `env:"PASSWORD"`
	Name                string `env:"NAME"`
	CloudSQLInstance    string `env:"CLOUD_SQL_INSTANCE"`
	DatabaseURLTemplate string `env:"URL_TEMPLATE" envDefault:"postgres:tc://[username]:[password]@[host]:[port]/[database_name]"`
}

// ResolveURL expands the DatabaseURLTemplate placeholders using the struct's own credential fields.
func (d DBConfig) ResolveURL() string {
	r := strings.NewReplacer(
		"[username]", d.User,
		"[password]", url.QueryEscape(d.Password),
		"[host]", d.Host,
		"[port]", strconv.Itoa(d.Port),
		"[database_name]", d.Name,
		"[query_parameters]", "",
	)
	return r.Replace(d.DatabaseURLTemplate)
}

// LogValue implements slog.LogValuer, redacting the password.
func (d DBConfig) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("host", d.Host),
		slog.Int("port", d.Port),
		slog.String("user", d.User),
		slog.String("password", "[REDACTED]"),
		slog.String("name", d.Name),
		slog.String("cloud_sql_instance", d.CloudSQLInstance),
		slog.String("url_template", d.DatabaseURLTemplate),
	)
}
