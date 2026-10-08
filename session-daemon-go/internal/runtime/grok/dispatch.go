package grok

import "fmt"

// A slow projection never blocks the RPC reader. Once full, fail the transport
// instead of dropping an update and continuing with an incomplete transcript.
func (c *client) queueEvent(event func()) bool {
	select {
	case <-c.eventStop:
		return false
	default:
	}
	select {
	case c.events <- event:
		return true
	case <-c.eventStop:
		return false
	default:
		c.fail(fmt.Errorf("grok notification queue overflow"))
		return false
	}
}
func (c *client) dispatchEvents() {
	defer close(c.eventDone)
	for {
		select {
		case <-c.eventStop:
			return
		case event := <-c.events:
			select {
			case <-c.eventStop:
				return
			default:
			}
			event()
		}
	}
}
