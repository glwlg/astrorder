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
	if err := d.ShowEmbeddedPage(ctx, "https://gateway.example/", StatisticsBounds{}, false); err == nil {
		t.Fatal("untrusted window can open embedded page")
	}
	if err := d.HideEmbeddedPage(ctx); err == nil {
		t.Fatal("untrusted window can hide embedded page")
	}
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
	if err := (StatisticsBounds{
		X: 68, Y: 48, Width: 1200, Height: 800, Scale: 1.25,
		Cutouts: []StatisticsCutout{
			{X: 74, Y: 210, Width: 280, Height: 180, Radius: 12},
			{X: 480, Y: 11, Width: 260, Height: 48, Radius: 24},
		},
	}).validate(); err != nil {
		t.Fatalf("valid cutouts rejected: %v", err)
	}
	for _, bad := range []StatisticsBounds{
		{Width: 100, Height: 100, Scale: 1, Cutouts: []StatisticsCutout{{Width: -5, Height: 10}}},
		{Width: 100, Height: 100, Scale: 1, Cutouts: []StatisticsCutout{{Width: 10, Height: 10, Radius: -1}}},
		{Width: 100, Height: 100, Scale: 1, Cutouts: []StatisticsCutout{{X: math.NaN(), Width: 10, Height: 10}}},
		{Width: 100, Height: 100, Scale: 1, Cutouts: make([]StatisticsCutout, maxStatisticsCutouts+1)},
	} {
		if bad.validate() == nil {
			t.Errorf("accepted invalid cutout bounds: %+v", bad)
		}
	}
}
