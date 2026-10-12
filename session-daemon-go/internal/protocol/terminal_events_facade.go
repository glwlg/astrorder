package protocol

import (
	"astrorder.dev/session-daemon/internal/events"
)

// SubscribeEvents provides direct subscription to the daemon journal events.
func (d *Daemon) SubscribeJournal(capacity int) *events.Subscription {
	return d.journal.SubscribeWithCapacity(capacity)
}

// UnsubscribeJournal removes a subscription from the journal.
func (d *Daemon) UnsubscribeJournal(sub *events.Subscription) {
	d.journal.Unsubscribe(sub)
}
