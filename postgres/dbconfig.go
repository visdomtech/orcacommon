package postgres

import (
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"strconv"
	"strings"

	"github.com/caarlos0/env/v11"
	"gopkg.in/yaml.v3"
)

// DBConfig holds the database connection parameters.
// Environment variables are read with the "DB_" prefix (e.g. DB_HOST, DB_PORT).
type DBConfig struct {
	Host                string `env:"HOST"                  envDefault:"localhost" yaml:"host"`
	Port                int    `env:"PORT"                  envDefault:"5432"      yaml:"port"`
	User                string `env:"USER"                                         yaml:"user"`
	Password            string `env:"PASSWORD"                                     yaml:"password"`
	Name                string `env:"NAME"                                         yaml:"name"`
	CloudSQLInstance    string `env:"CLOUD_SQL_INSTANCE"                           yaml:"cloud_sql_instance"`
	// MigrationSchema sets the PostgreSQL search_path for atlas migrations.
	// Switching from the default "public" to another schema creates a new
	// atlas_schema_revisions table in that schema; existing migration history
	// in "public" is not carried over.
	MigrationSchema     string `env:"MIGRATION_SCHEMA"      envDefault:"public"    yaml:"migration_schema"`
	// RebaselineLastMigration, when non-empty, enables the rebaseline
	// operation. It specifies the version of the last applied migration
	// that must match the latest revision in the history table and the
	// single migration file in the directory. An empty value (the default)
	// preserves all current migration behavior.
	RebaselineLastMigration string `env:"REBASELINE_LAST_MIGRATION"                yaml:"rebaseline_last_migration"`
	DatabaseURLTemplate string `env:"URL_TEMPLATE" envDefault:"postgres:tc://[username]:[password]@[host]:[port]/[database_name]" yaml:"url_template"`
}

// LoadDBConfig reads default values from a YAML file within fsys, then
// overlays environment variables (with the "DB_" prefix). The precedence order
// is: YAML file → env vars → envDefault struct tags.
//
// The fsys parameter accepts any fs.FS implementation, including go:embed
// filesystems (e.g. LoadDBConfig(embeddedConfigFS, "db.yaml")) and
// os.DirFS for file-system paths.
func LoadDBConfig(fsys fs.FS, name string) (*DBConfig, error) {
	var cfg DBConfig

	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, fmt.Errorf("load db config %s: %w", name, err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse db config yaml %s: %w", name, err)
	}
	if err := env.Parse(&cfg); err != nil {
		return nil, fmt.Errorf("parse db config env: %w", err)
	}

	return &cfg, nil
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

// IsEmbeddedPostgres reports whether dbURL starts with the "postgres:embedded:" prefix.
func IsEmbeddedPostgres(dbURL string) bool {
	return strings.HasPrefix(dbURL, "postgres:embedded:")
}

// IsTestContainer reports whether dbURL starts with the "postgres:tc:" prefix.
func IsTestContainer(dbURL string) bool {
	return strings.HasPrefix(dbURL, "postgres:tc:")
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
		slog.String("migration_schema", d.MigrationSchema),
		slog.String("rebaseline_last_migration", d.RebaselineLastMigration),
		slog.String("url_template", d.DatabaseURLTemplate),
	)
}
