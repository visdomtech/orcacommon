package litespaserver

import (
	"context"
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
		slog.WarnContext(ctx, "litespaserver: reload version from db failed, using cached", "err", err)
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

// NewManager builds a Manager. When cdnVersion is non-empty the version is
// locked to it (static provider) and the DB is not touched; otherwise the
// defaultVersion is seeded into the DB if absent and a DB-backed provider is
// used. cdn is the CDN prefix used to validate candidate versions.
// When embedded is true, a static provider with version "embedded" is used,
// bypassing the DB entirely.
func NewManager(ctx context.Context, pool *pgxpool.Pool, cdn, cdnVersion, defaultVersion string, embedded bool) *Manager {
	d := &dao{pool: pool}
	m := &Manager{
		cdn:     cdn,
		dao:     d,
		fetcher: newFetcher(nil),
	}

	if embedded {
		slog.InfoContext(ctx, "litespaserver: embedded content mode, version locked to 'embedded'")
		m.provider = &staticProvider{v: "embedded"}
		return m
	}

	if cdnVersion != "" {
		slog.WarnContext(ctx, "litespaserver: version locked by configuration, DB value ignored", "version", cdnVersion)
		m.provider = &staticProvider{v: cdnVersion}
		return m
	}

	m.seedDefaultIfAbsent(ctx, defaultVersion)
	m.provider = &dbProvider{dao: d}
	return m
}

// seedDefaultIfAbsent persists defaultVersion when the litespa_settings table
// has no frontend version yet.
func (m *Manager) seedDefaultIfAbsent(ctx context.Context, defaultVersion string) {
	v, err := m.dao.getVersion(ctx)
	if err != nil {
		slog.WarnContext(ctx, "litespaserver: check existing version failed", "err", err)
		return
	}
	if v == "" && defaultVersion != "" {
		slog.InfoContext(ctx, "litespaserver: version missing in DB, seeding default", "version", defaultVersion)
		if err := m.dao.setVersion(ctx, defaultVersion); err != nil {
			slog.WarnContext(ctx, "litespaserver: seed default version failed", "err", err)
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
// and persisted; false when validation or the DB write failed.
func (m *Manager) SetVersion(ctx context.Context, candidate string) bool {
	if !m.isValidVersion(ctx, candidate) {
		return false
	}
	if err := m.dao.setVersion(ctx, candidate); err != nil {
		slog.WarnContext(ctx, "litespaserver: persist new version failed", "version", candidate, "err", err)
		return false
	}
	slog.InfoContext(ctx, "litespaserver: persisted new version", "version", candidate)
	m.provider.refresh(ctx)
	m.notifyListeners()
	return true
}

// OnChange registers a callback fired after a successful SetVersion.
func (m *Manager) OnChange(fn func()) {
	m.mu.Lock()
	m.listeners = append(m.listeners, fn)
	m.mu.Unlock()
}

func (m *Manager) notifyListeners() {
	m.mu.Lock()
	listeners := append([]func(){}, m.listeners...)
	m.mu.Unlock()
	for _, fn := range listeners {
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Warn("litespaserver: version-change listener panicked", "recover", r)
				}
			}()
			fn()
		}()
	}
}

func (m *Manager) isValidVersion(ctx context.Context, candidate string) bool {
	if candidate == "" {
		slog.InfoContext(ctx, "litespaserver: candidate version is blank")
		return false
	}
	if _, ok := m.fetcher.fetch(ctx, m.cdn, candidate); !ok {
		slog.InfoContext(ctx, "litespaserver: candidate version not fetchable, not published yet?", "version", candidate)
		return false
	}
	return true
}
