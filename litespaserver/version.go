package litespaserver

import (
	"context"
	"io/fs"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/jackc/pgx/v5/pgxpool"
)

// dbCacheTTL is how long the database-backed provider serves a cached version
// before reloading.
const dbCacheTTL = 5 * time.Minute

// versionProvider sources the live frontend version.
type versionProvider interface {
	version(ctx context.Context) string
	refresh(ctx context.Context)
}

// staticProvider serves a fixed version from configuration.
type staticProvider struct{ v string }

func (p *staticProvider) version(context.Context) string { return p.v }
func (p *staticProvider) refresh(context.Context)        {}

// dbProvider serves the version from the litespa_settings table with a TTL cache.
type dbProvider struct {
	dao *dao
	sf  singleflight.Group

	mu       sync.RWMutex
	cached   string
	loadedAt time.Time
}

func (p *dbProvider) version(ctx context.Context) string {
	p.mu.RLock()
	v, loadedAt := p.cached, p.loadedAt
	p.mu.RUnlock()

	if v != "" && time.Since(loadedAt) < dbCacheTTL {
		return v
	}
	// Collapse concurrent TTL-expiry reloads to a single DB query.
	result, _, _ := p.sf.Do("reload", func() (any, error) {
		return p.reload(ctx, v), nil
	})
	return result.(string)
}

func (p *dbProvider) refresh(ctx context.Context) {
	p.mu.RLock()
	prev := p.cached
	p.mu.RUnlock()
	p.sf.Do("reload", func() (any, error) {
		return p.reload(ctx, prev), nil
	})
}

// reload fetches the version from the DB and updates the cache. On error it
// keeps serving the previous value (fallback) so a transient DB blip does not
// break page serving.
func (p *dbProvider) reload(ctx context.Context, fallback string) string {
	v, err := p.dao.getVersion(ctx)
	if err != nil {
		slog.WarnContext(ctx, "litespaserver: reload version from db failed, using cached", "key", p.dao.key, "err", err)
		return fallback
	}
	p.mu.Lock()
	p.cached = v
	p.loadedAt = time.Now()
	p.mu.Unlock()
	return v
}

// Manager owns frontend version resolution.
type Manager struct {
	cdn      string
	dao      *dao
	fetcher  *fetcher
	provider versionProvider

	mu        sync.Mutex
	listeners []func()
}

// NewManager builds a Manager from the provided Config. When cfg.CDNVersion
// is non-empty the version is locked to it (static provider) and the DB is
// not touched; otherwise cfg.DefaultVersion is seeded into the DB if absent
// and a DB-backed provider is used. cfg.CDNPrefix is the CDN prefix used to
// validate candidate versions. When embedded is non-nil (and contains
// index.html), a static provider with version "embedded" is used, bypassing
// the DB entirely. cfg.FrontendName namespaces the version key in the
// litespa_settings table so multiple SPAs can share the same database.
//
// The embedded parameter should be pre-resolved by the caller (e.g. NewServer)
// to avoid duplicate resolveEmbedded calls.
func NewManager(ctx context.Context, pool *pgxpool.Pool, cfg Config, embedded fs.FS) *Manager {
	d := &dao{pool: pool, key: versionKey(cfg.FrontendName)}
	if cfg.FrontendName != "" {
		slog.InfoContext(ctx, "litespaserver: version key namespaced", "key", d.key, "frontendName", cfg.FrontendName)
	}
	m := &Manager{
		cdn:     cfg.CDNPrefix,
		dao:     d,
		fetcher: newFetcher(nil),
	}

	if embedded != nil {
		slog.InfoContext(ctx, "litespaserver: embedded content mode", "version", "embedded", "key", d.key)
		m.provider = &staticProvider{v: "embedded"}
		return m
	}

	if cfg.CDNVersion != "" {
		slog.WarnContext(ctx, "litespaserver: version locked by configuration, DB value ignored", "key", d.key, "version", cfg.CDNVersion)
		m.provider = &staticProvider{v: cfg.CDNVersion}
		return m
	}

	// Guard against nil pool — fall back to static provider with DefaultVersion.
	if pool == nil {
		slog.WarnContext(ctx, "litespaserver: no pool provided, falling back to static provider", "key", d.key, "version", cfg.DefaultVersion)
		m.provider = &staticProvider{v: cfg.DefaultVersion}
		return m
	}

	m.seedDefaultIfAbsent(ctx, cfg.DefaultVersion)
	m.provider = &dbProvider{dao: d}
	return m
}

// seedDefaultIfAbsent persists defaultVersion when the litespa_settings table
// has no frontend version yet.
func (m *Manager) seedDefaultIfAbsent(ctx context.Context, defaultVersion string) {
	v, err := m.dao.getVersion(ctx)
	if err != nil {
		slog.WarnContext(ctx, "litespaserver: check existing version failed", "key", m.dao.key, "err", err)
		return
	}
	if v == "" && defaultVersion != "" {
		slog.InfoContext(ctx, "litespaserver: version missing in DB, seeding default", "key", m.dao.key, "version", defaultVersion)
		if err := m.dao.setVersion(ctx, defaultVersion); err != nil {
			slog.WarnContext(ctx, "litespaserver: seed default version failed", "key", m.dao.key, "err", err)
		}
	}
}

// Version returns the current frontend version.
func (m *Manager) Version(ctx context.Context) string {
	return m.provider.version(ctx)
}

// ForceRefresh reloads the version from the DB.
func (m *Manager) ForceRefresh(ctx context.Context) {
	m.provider.refresh(ctx)
}

// SetVersion validates a candidate version against the CDN, then persists it and
// refreshes the cache and listeners. Returns true when the version was accepted
// and persisted; false when validation or the DB write failed. Returns false
// immediately when the provider is static (CDNVersion or embedded).
func (m *Manager) SetVersion(ctx context.Context, candidate string) bool {
	if _, ok := m.provider.(*staticProvider); ok {
		slog.InfoContext(ctx, "litespaserver: SetVersion ignored, version is statically configured", "key", m.dao.key)
		return false
	}
	if !m.isValidVersion(ctx, candidate) {
		return false
	}
	if err := m.dao.setVersion(ctx, candidate); err != nil {
		slog.WarnContext(ctx, "litespaserver: persist new version failed", "key", m.dao.key, "version", candidate, "err", err)
		return false
	}
	slog.InfoContext(ctx, "litespaserver: persisted new version", "key", m.dao.key, "version", candidate)
	m.provider.refresh(ctx)
	m.notifyListeners(ctx)
	return true
}

// OnChange registers a callback fired after a successful SetVersion.
func (m *Manager) OnChange(fn func()) {
	m.mu.Lock()
	m.listeners = append(m.listeners, fn)
	m.mu.Unlock()
}

func (m *Manager) notifyListeners(ctx context.Context) {
	m.mu.Lock()
	listeners := append([]func(){}, m.listeners...)
	m.mu.Unlock()
	for _, fn := range listeners {
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.WarnContext(ctx, "litespaserver: version-change listener panicked", "key", m.dao.key, "recover", r)
				}
			}()
			fn()
		}()
	}
}

func (m *Manager) isValidVersion(ctx context.Context, candidate string) bool {
	if candidate == "" {
		slog.InfoContext(ctx, "litespaserver: candidate version is blank", "key", m.dao.key)
		return false
	}
	if _, ok := m.fetcher.fetch(ctx, m.cdn, candidate); !ok {
		slog.InfoContext(ctx, "litespaserver: candidate version not fetchable, not published yet?", "key", m.dao.key, "version", candidate)
		return false
	}
	return true
}
