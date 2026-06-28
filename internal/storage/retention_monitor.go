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

	lifecycleMu sync.Mutex
	mu          sync.Mutex
	cancel      context.CancelFunc
	wg          sync.WaitGroup
}

var onRetentionMonitorLoopStart = func(context.Context) {}
var onRetentionMonitorSweepComplete = func(context.Context, error) {}

func NewRetentionMonitor(fs *FileSystem, cfg RetentionMonitorConfig, logger zerolog.Logger) *RetentionMonitor {
	return &RetentionMonitor{fs: fs, cfg: cfg, logger: logger}
}

func (m *RetentionMonitor) Start(ctx context.Context) {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()

	m.mu.Lock()
	m.baseCtx = ctx
	cfg := m.cfg
	m.mu.Unlock()

	m.restartLocked(cfg, func() {
		if m.fs != nil {
			m.fs.UpdateRetentionSettings(cfg.MaxVersions, cfg.MaxVersionAge, cfg.MinFreeSpace, cfg.SweepInterval)
		}
	})
}

func (m *RetentionMonitor) Stop() {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()

	m.stopLoopLocked()
	m.wg.Wait()
}

func (m *RetentionMonitor) UpdateConfig(cfg RetentionMonitorConfig) {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()

	m.restartLocked(cfg, func() {
		if m.fs != nil {
			m.fs.UpdateRetentionSettings(cfg.MaxVersions, cfg.MaxVersionAge, cfg.MinFreeSpace, cfg.SweepInterval)
		}
	})
}

// UpdateConfigAndRuntimePolicy stops the active sweep, publishes the complete
// runtime policy atomically, and then starts the replacement sweep loop.
func (m *RetentionMonitor) UpdateConfigAndRuntimePolicy(cfg RetentionMonitorConfig, policy RuntimePolicySettings) {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()

	m.restartLocked(cfg, func() {
		if m.fs != nil {
			m.fs.UpdateRuntimePolicySettings(policy)
		}
	})
}

func (m *RetentionMonitor) restartLocked(cfg RetentionMonitorConfig, publishPolicy func()) {
	m.stopLoopLocked()
	m.wg.Wait()
	if publishPolicy != nil {
		publishPolicy()
	}

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
		onRetentionMonitorLoopStart(loopCtx)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-loopCtx.Done():
				return
			case <-ticker.C:
				err := m.fs.RunRetentionSweep(loopCtx)
				if err != nil {
					m.logger.Warn().Err(err).Msg("Retention sweep failed")
				}
				onRetentionMonitorSweepComplete(loopCtx, err)
			}
		}
	}(cfg.SweepInterval)

	m.logger.Info().Dur("interval", cfg.SweepInterval).Msg("Retention monitor started")
}

func (m *RetentionMonitor) stopLoopLocked() {
	m.mu.Lock()
	cancel := m.cancel
	m.cancel = nil
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
}
