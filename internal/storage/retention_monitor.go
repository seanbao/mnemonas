package storage

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// RetentionMonitorConfig controls background retention sweeps.
type RetentionMonitorConfig struct {
	MaxVersions   int
	MaxVersionAge time.Duration
	MinFreeSpace  uint64
	SweepInterval time.Duration
}

// RetentionMonitor runs periodic retention sweeps and applies runtime config updates.
type RetentionMonitor struct {
	fs      *FileSystem
	logger  zerolog.Logger
	baseCtx context.Context
	cfg     RetentionMonitorConfig

	mu     sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewRetentionMonitor(fs *FileSystem, cfg RetentionMonitorConfig, logger zerolog.Logger) *RetentionMonitor {
	return &RetentionMonitor{fs: fs, cfg: cfg, logger: logger}
}

func (m *RetentionMonitor) Start(ctx context.Context) {
	m.mu.Lock()
	m.baseCtx = ctx
	cfg := m.cfg
	m.mu.Unlock()

	m.restart(cfg)
}

func (m *RetentionMonitor) Stop() {
	m.stopLoop()
	m.wg.Wait()
}

func (m *RetentionMonitor) UpdateConfig(cfg RetentionMonitorConfig) {
	if m.fs != nil {
		m.fs.UpdateRetentionSettings(cfg.MaxVersions, cfg.MaxVersionAge, cfg.MinFreeSpace)
	}
	m.restart(cfg)
}

func (m *RetentionMonitor) restart(cfg RetentionMonitorConfig) {
	m.stopLoop()
	m.wg.Wait()

	m.mu.Lock()
	m.cfg = cfg
	baseCtx := m.baseCtx
	m.mu.Unlock()

	if baseCtx == nil || m.fs == nil {
		return
	}
	if cfg.SweepInterval <= 0 {
		m.logger.Info().Msg("Retention monitor disabled")
		return
	}

	loopCtx, cancel := context.WithCancel(baseCtx)

	m.mu.Lock()
	m.cancel = cancel
	m.wg.Add(1)
	m.mu.Unlock()

	go func(interval time.Duration) {
		defer m.wg.Done()

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-loopCtx.Done():
				return
			case <-ticker.C:
				if err := m.fs.RunRetentionSweep(loopCtx); err != nil {
					m.logger.Warn().Err(err).Msg("Retention sweep failed")
				}
			}
		}
	}(cfg.SweepInterval)

	m.logger.Info().Dur("interval", cfg.SweepInterval).Msg("Retention monitor started")
}

func (m *RetentionMonitor) stopLoop() {
	m.mu.Lock()
	cancel := m.cancel
	m.cancel = nil
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
}
