package main

import (
	"context"
	"github.com/wailsapp/wails/v3/pkg/application"
	"math"
	"testing"
)

func TestStatisticsCannotCallDesktopCapabilities(t *testing.T) {
	d := &DesktopService{shell: &ShellService{}}
	ctx := context.WithValue(context.Background(), application.WindowKey, &application.WebviewWindow{})
	if err := d.ShowStatistics(ctx, "https://gateway.example/", StatisticsBounds{}, false); err == nil {
		t.Fatal("untrusted window can open statistics")
	}
	if err := d.HideStatistics(ctx); err == nil {
		t.Fatal("untrusted window can hide statistics")
	}
	if _, err := d.Run(ctx, "daemon", "stop"); err == nil {
		t.Fatal("untrusted window can stop daemon")
	}
}

func TestStatisticsAddressAndBounds(t *testing.T) {
	for _, address := range []string{"javascript:alert(1)", "file:///C:/windows", "https://user:pass@example.test/", "https://example.test/\n"} {
		if validateStatisticsAddress(address) == nil {
			t.Errorf("accepted invalid statistics address")
		}
	}
	if err := validateStatisticsAddress("https://gateway.example/?k=fixture-only#usage"); err != nil {
		t.Fatal(err)
	}
	for _, bounds := range []StatisticsBounds{{Width: math.NaN()}, {Width: -1}, {Width: 1, Height: 1, Scale: math.Inf(1)}} {
		if bounds.validate() == nil {
			t.Errorf("accepted invalid bounds")
		}
	}
	if err := (StatisticsBounds{X: 100, Y: 30, Width: 800, Height: 600, Scale: 1.5}).validate(); err != nil {
		t.Fatal(err)
	}
}
