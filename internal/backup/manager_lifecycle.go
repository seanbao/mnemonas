package backup

import "context"

func (m *Manager) beginManagerOperation() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return false
	}
	if err := m.verifyStateNamespaceLocked(); err != nil {
		_ = m.quarantineStateNamespaceLocked(err, err)
		return false
	}
	m.operations.Add(1)
	return true
}

func (m *Manager) endManagerOperation() {
	m.operations.Done()
}

// Close stops background work, waits for active backup operations, and
// releases the process-wide backup state lock. It is safe to call repeatedly.
func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		cancel := m.schedulerCancel
		done := m.schedulerDone
		shutdownCancel := m.shutdownCancel
		m.mu.Unlock()

		if shutdownCancel != nil {
			shutdownCancel()
		}
		if cancel != nil {
			cancel()
		}
		if done != nil {
			<-done
		}
		m.operations.Wait()
		m.closeErr = m.stateLock.Close()
	})
	return m.closeErr
}

func (m *Manager) notificationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	notificationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultNotificationTimeout)
	if m == nil || m.shutdownCtx == nil {
		return notificationCtx, cancel
	}
	stopShutdownCancel := context.AfterFunc(m.shutdownCtx, cancel)
	return notificationCtx, func() {
		stopShutdownCancel()
		cancel()
	}
}
