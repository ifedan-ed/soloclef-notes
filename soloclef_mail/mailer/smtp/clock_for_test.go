package smtp

import "time"

// AdvanceClockForTest moves the session's own clock on, so the idle check can
// be tested without waiting fifteen seconds. smtp_connection.py takes its
// clock as a constructor argument for the same reason.
func (m *Mailer) AdvanceClockForTest(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	offset := d
	base := m.session.now
	if base == nil {
		base = time.Now
	}
	m.session.now = func() time.Time { return base().Add(offset) }
}
