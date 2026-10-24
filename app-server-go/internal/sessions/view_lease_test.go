package sessions

import (
	"testing"
	"time"
)

func TestViewLeaseExpiryAndRenewal(t *testing.T) {
	var v viewLeases
	now := time.Unix(1000, 0)
	if v.active("a::s", now) {
		t.Fatal("unexpected lease")
	}
	v.touch("a::s", now)
	if !v.active("a::s", now.Add(time.Minute)) {
		t.Fatal("view expired too early")
	}
	if v.active("b::s", now) {
		t.Fatal("lease crossed agent boundary")
	}
	v.touch("a::s", now.Add(time.Minute))
	if !v.active("a::s", now.Add(100*time.Second)) {
		t.Fatal("renewal lost")
	}
	if v.active("a::s", now.Add(150*time.Second)) {
		t.Fatal("expired lease is active")
	}
	v.touch("b::s", now.Add(151*time.Second))
	if len(v.until) != 1 {
		t.Fatal("expired leases were not reclaimed")
	}
}
